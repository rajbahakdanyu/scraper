package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/session"
)

var errNoRun = errors.New("no run in progress")

// group is one same-type slice of a run (YouTube first, then other sites),
// each with its own downloader — the same split the CLI makes.
type group struct {
	build func() (*session.Session, error)
}

// runner owns one run's sessions. A single goroutine (loop) mutates them;
// bound API calls and finished tasks reach it through ops.
type runner struct {
	app    *App
	cfg    *config.Config
	hooks  app.Hooks
	groups []group
	batch  bool
	// auto finishes each group as soon as it settles (watch mode), the
	// way the CLI's non-interactive runs do.
	auto bool

	idx     int
	sess    *session.Session
	results []error

	ops      chan func()
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	finished bool
	version  uint64
	emitted  uint64
	ticks    int

	logMu   sync.Mutex
	logFile *os.File
	onEnd   func(error)
}

func (r *runner) start() {
	r.ops = make(chan func())
	r.done = make(chan struct{})
	r.ctx, r.cancel = context.WithCancel(context.Background())
	go r.loop()
}

func (r *runner) loop() {
	defer close(r.done)
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	r.startGroup(0)
	for !r.finished {
		select {
		case f := <-r.ops:
			f()
		case <-t.C:
			r.tick()
		case <-r.ctx.Done():
			r.stop()
		}
		r.emitIfChanged()
	}
}

// call runs f on the loop and waits for it; after the run ends, f runs
// directly since nothing mutates the session any more.
func (r *runner) call(f func()) {
	ran := make(chan struct{})
	select {
	case r.ops <- func() { f(); close(ran) }:
		<-ran
	case <-r.done:
		f()
	}
}

func (r *runner) bump() { r.version++ }

// ended reports whether the run's loop has exited.
func (r *runner) ended() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

func (r *runner) spawn(s *session.Session, tasks []session.Task) {
	for _, t := range tasks {
		go func() {
			res := s.Exec(t)
			select {
			case r.ops <- func() {
				if r.sess == s {
					r.spawn(s, s.Apply(res))
					r.bump()
				}
			}:
			case <-r.done:
			}
		}()
	}
}

func (r *runner) tick() {
	s := r.sess
	if s == nil {
		return
	}
	r.ticks++
	if r.ticks%5 == 0 {
		s.Tick()
	}
	before := s.Phase
	if s.Refresh() || s.Phase != before {
		r.bump()
	}
	if r.auto && r.groupOver() {
		r.finishGroup()
	}
}

// groupOver reports a group with nothing left to do: settled into the
// summary, or ended without items (every scrape failed).
func (r *runner) groupOver() bool {
	s := r.sess
	return s != nil && (s.Phase == session.PhaseSummary || (s.Phase == session.PhaseDone && !s.Active()))
}

func (r *runner) startGroup(i int) {
	for ; i < len(r.groups); i++ {
		r.idx = i
		s, err := r.groups[i].build()
		if err != nil {
			r.results = append(r.results, err)
			r.app.notice(err.Error())
			r.app.host.Emit("run:group", GroupResult{Group: i, Error: err.Error()})
			continue
		}
		r.sess = s
		r.spawn(s, s.Start())
		r.bump()
		return
	}
	r.finish()
}

// finishGroup ends the current group like quitting a TUI dashboard: stop
// its downloads, write the retry list, notify, then move to the next group.
func (r *runner) finishGroup() {
	s := r.sess
	if s == nil {
		return
	}
	s.Shutdown()
	res := s.Result()
	r.results = append(r.results, res)
	gr := GroupResult{Group: r.idx, Error: errText(res)}
	rep, err := app.WriteRetryReport(r.cfg.OutputDir, s.Failures(), time.Now())
	if err != nil {
		r.app.notice(fmt.Sprintf("retry list: %v", err))
	} else if rep.Path != "" {
		gr.RetryFile, gr.Summary = rep.Path, rep.Summary
		r.logf("retry list: %s (%d failed item(s), %d line(s))", rep.Path, rep.Failed, rep.Lines)
	}
	r.app.host.Emit("run:group", gr)
	if r.cfg.Notify {
		if err := app.Notify(app.NotificationSummary(res)); err != nil {
			r.app.notice(fmt.Sprintf("notification: %v", err))
		}
	}
	r.bump()
	r.startGroup(r.idx + 1)
}

// stop ends the current group and skips the rest.
func (r *runner) stop() {
	if r.finished {
		return
	}
	r.groups = r.groups[:min(len(r.groups), r.idx+1)]
	if r.sess != nil {
		r.finishGroup()
	} else {
		r.finish()
	}
}

func (r *runner) finish() {
	if r.finished {
		return
	}
	r.finished = true
	var err error
	switch {
	case r.batch:
		err = model.NewPartialError("batch", r.results)
	case len(r.results) > 0:
		err = r.results[0]
	}
	if err != nil {
		r.logf("error: %v", err)
	}
	r.closeLog()
	var partial *model.PartialError
	r.bump()
	r.app.host.Emit("run:finished", RunResult{Error: errText(err), Partial: errors.As(err, &partial)})
	if r.onEnd != nil {
		r.onEnd(err)
	}
}

func (r *runner) emitIfChanged() {
	if r.version == r.emitted {
		return
	}
	r.emitted = r.version
	r.app.host.Emit("run:update", r.state())
}

func (r *runner) state() RunState {
	st := RunState{Running: !r.finished, Groups: len(r.groups), Group: r.idx, Version: r.version}
	s := r.sess
	if s == nil {
		return st
	}
	st.Phase = phaseNames[s.Phase]
	st.Site, st.Dest = s.Site, s.Dest
	st.LoadingErr = s.LoadingErr
	st.Status, st.StatusSeq = s.Status, s.StatusSeq
	st.Filter = s.Filter.String()
	st.Visible = len(s.Visible())
	st.Stats = statsView(s.Stats())
	st.BatchMode = s.BatchMode
	st.AlbumsTotal = len(s.Albums)
	st.AlbumsScraped = len(s.Albums) - s.BatchRemaining
	if s.BatchMode && s.BatchIdx < len(s.Albums) && s.BatchRemaining > 0 {
		a := s.Albums[s.BatchIdx]
		st.ScrapingAlbum = fmt.Sprintf("%s (%s)", a.Slug, a.Site)
	}
	st.Errors = len(s.ErrorLog)
	st.Active = s.Active()
	return st
}

func (r *runner) logf(format string, args ...any) {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	if r.logFile != nil {
		app.WriteLogLine(r.logFile, time.Now(), fmt.Sprintf(format, args...))
	}
}

func (r *runner) closeLog() {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	if r.logFile != nil {
		_ = r.logFile.Close()
		r.logFile = nil
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
