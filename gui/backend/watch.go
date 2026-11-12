package backend

import (
	"errors"
	"fmt"
	"time"
)

const watchPollInterval = time.Minute

// watcher re-runs the same input every interval, like --watch: groups
// finish on their own and failures never stop the loop.
type watcher struct {
	in       RunInput
	interval time.Duration
	timer    *time.Timer
	next     time.Time
}

// StartWatch runs the input now and again every interval (e.g. "10m")
// until StopWatch.
func (a *App) StartWatch(in RunInput, interval string) error {
	d, err := time.ParseDuration(interval)
	if err != nil || d <= 0 {
		return fmt.Errorf("invalid watch interval %q (e.g. 30m, 1h)", interval)
	}
	w := &watcher{in: in, interval: d}
	a.mu.Lock()
	if a.watch != nil {
		a.mu.Unlock()
		return errors.New("already watching")
	}
	a.watch = w
	a.mu.Unlock()
	if err := a.watchRun(w); err != nil {
		a.mu.Lock()
		a.watch = nil
		a.mu.Unlock()
		return err
	}
	return nil
}

func (a *App) watchRun(w *watcher) error {
	return a.startRun(w.in, true, func(err error) {
		if err != nil {
			a.notice(fmt.Sprintf("watch: %v", err))
		}
		a.scheduleWatch(w)
	})
}

func (a *App) scheduleWatch(w *watcher) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.watch != w {
		return
	}
	w.next = time.Now().Add(w.interval).Round(0)
	a.scheduleWatchCheckLocked(w)
	if a.host != nil {
		a.host.Emit("watch:next", w.next.Format(time.RFC3339))
	}
}

func watchCheckDelay(now, next time.Time) time.Duration {
	delay := next.Sub(now)
	if delay <= 0 {
		return 0
	}
	if delay > watchPollInterval {
		return watchPollInterval
	}
	return delay
}

func (a *App) scheduleWatchCheckLocked(w *watcher) {
	delay := watchCheckDelay(time.Now(), w.next)
	w.timer = time.AfterFunc(delay, func() {
		a.mu.Lock()
		if a.watch != w {
			a.mu.Unlock()
			return
		}
		if time.Now().Before(w.next) {
			a.scheduleWatchCheckLocked(w)
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		if err := a.watchRun(w); err != nil {
			a.notice(fmt.Sprintf("watch: %v", err))
			a.scheduleWatch(w)
		}
	})
}

// StopWatch stops re-running and ends the current run.
func (a *App) StopWatch() {
	a.mu.Lock()
	w := a.watch
	a.watch = nil
	a.mu.Unlock()
	if w == nil {
		return
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	a.StopRun()
	if a.host != nil {
		a.host.Emit("watch:stopped", nil)
	}
}
