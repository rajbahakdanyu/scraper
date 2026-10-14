// Package backend is the Go side of the desktop GUI: the methods of App
// are bound to the frontend, and run state is pushed to it as events.
package backend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/parser/youtube"
	"github.com/rajbahakdanyu/scraper/internal/session"
)

// maxNotices bounds the notice log shown in the GUI.
const maxNotices = 200

// App is bound to the frontend; every exported method is callable from JS.
type App struct {
	host       host
	configPath string

	mu       sync.Mutex
	startMu  sync.Mutex
	settings Settings
	run      *runner
	watch    *watcher
	notices  []string
	preview  context.CancelFunc

	// Test seams for the network-facing constructors.
	newScraper    func(site, proxyURL string, cookies []*http.Cookie) (model.Scraper, error)
	registry      func(proxyURL string, cookies []*http.Cookie) (map[string]model.Scraper, error)
	ytScraper     func() model.Scraper
	newDownloader func(scraper model.Scraper, site string, cfg *config.Config, hooks app.Hooks) (model.Downloader, error)
}

// New creates the backend for the config file at configPath.
func New(configPath string, h host) *App {
	a := &App{
		host:          h,
		configPath:    configPath,
		newScraper:    app.NewScraper,
		registry:      app.Registry,
		ytScraper:     func() model.Scraper { return youtube.NewYouTubeScraper() },
		newDownloader: app.BuildDownloader,
	}
	cfg, err := a.loadConfig()
	if err != nil {
		a.notice(fmt.Sprintf("config: %v (using defaults)", err))
		cfg = config.Default()
	}
	a.settings = settingsFromConfig(cfg)
	return a
}

// loadConfig reads the config file; a missing file means the defaults.
func (a *App) loadConfig() (*config.Config, error) {
	if _, err := os.Stat(a.configPath); errors.Is(err, os.ErrNotExist) {
		return config.Default(), nil
	}
	return config.Load(a.configPath)
}

// Run starts the desktop app serving the built frontend from assets.
func Run(assets fs.FS) error {
	a := New(config.DefaultPath(), nil)
	return wails.Run(&options.App{
		Title:            "Scraper",
		Width:            1180,
		Height:           780,
		MinWidth:         820,
		MinHeight:        560,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 14, G: 17, B: 23, A: 1},
		OnStartup:        func(ctx context.Context) { a.host = wailsHost{ctx: ctx} },
		OnBeforeClose:    func(context.Context) bool { return a.beforeClose() },
		OnShutdown:       func(context.Context) { a.stopEverything() },
		Bind:             []any{a},
	})
}

// beforeClose asks before quitting over running downloads, then stops
// them the way quitting the TUI does.
func (a *App) beforeClose() bool {
	if a.runActive() && !a.host.Confirm("Quit Scraper?", "Downloads are still running. Quit and cancel them?") {
		return true
	}
	a.stopEverything()
	return false
}

func (a *App) stopEverything() {
	a.StopWatch()
	a.StopRun()
}

func (a *App) runActive() bool {
	r := a.current()
	if r == nil {
		return false
	}
	active := false
	r.call(func() { active = !r.finished && r.sess != nil && r.sess.Active() })
	return active
}

func (a *App) current() *runner {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.run
}

// notice records an in-run warning (filters, caps, disk space). Safe on
// any goroutine.
func (a *App) notice(msg string) {
	a.mu.Lock()
	a.notices = append(a.notices, msg)
	if len(a.notices) > maxNotices {
		a.notices = a.notices[len(a.notices)-maxNotices:]
	}
	h := a.host
	a.mu.Unlock()
	if h != nil {
		h.Emit("notice", msg)
	}
}

// --- Settings ---

// GetOptions returns the fixed choices for the settings form.
func (a *App) GetOptions() Options {
	return Options{
		Sites:        app.Sites(),
		Qualities:    app.Qualities,
		AudioFormats: app.AudioFormats,
		Browsers:     []string{"firefox"},
		ConfigPath:   a.configPath,
	}
}

// GetSettings returns the settings the next run uses.
func (a *App) GetSettings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// ApplySettings validates s and uses it for the next run without saving.
func (a *App) ApplySettings(s Settings) error {
	if err := s.validate(a.loadBase()); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	return nil
}

// SaveSettings validates s, uses it, and writes it to the config file.
func (a *App) SaveSettings(s Settings) error {
	if err := a.ApplySettings(s); err != nil {
		return err
	}
	base := a.loadBase()
	if err := s.apply(base); err != nil {
		return err
	}
	return config.Save(a.configPath, base)
}

// ReloadSettings discards unsaved changes by re-reading the config file.
func (a *App) ReloadSettings() (Settings, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return a.GetSettings(), err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings = settingsFromConfig(cfg)
	return a.settings, nil
}

func (a *App) loadBase() *config.Config {
	cfg, err := a.loadConfig()
	if err != nil {
		a.notice(fmt.Sprintf("config: %v (using defaults)", err))
		return config.Default()
	}
	return cfg
}

// runConfig builds the finalized config for a run from the current
// settings, importing browser cookies when configured.
func (a *App) runConfig() (*config.Config, error) {
	cfg := a.loadBase()
	if err := a.GetSettings().apply(cfg); err != nil {
		return nil, err
	}
	if err := app.FinalizeConfig(cfg); err != nil {
		return nil, err
	}
	if _, err := config.ParseRateLimit(cfg.RateLimit); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Notices returns the recent in-run warnings.
func (a *App) Notices() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.notices...)
}

// ClearNotices empties the notice log.
func (a *App) ClearNotices() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notices = nil
}

// --- Input ---

// RunInput is what the user asked to download.
type RunInput struct {
	// Text holds one URL per line, optionally "item URL<TAB>album URL"
	// (the retry-list format); # comments and blank lines are skipped.
	Text string `json:"text"`
	// FromFile marks input imported from a list file: like --input-file,
	// even a single line runs as a batch.
	FromFile bool `json:"fromFile"`
	// Site forces the site for a single URL, like the per-site
	// subcommands; empty auto-detects.
	Site string `json:"site"`
}

// DetectedURL is one parsed input line with its detected site.
type DetectedURL struct {
	URL      string `json:"url"`
	AlbumURL string `json:"albumUrl"`
	Site     string `json:"site"`
	Error    string `json:"error"`
}

// DetectSites parses input text and detects each line's site.
func (a *App) DetectSites(text string) []DetectedURL {
	entries, _ := app.ParseURLList(strings.NewReader(text))
	out := make([]DetectedURL, 0, len(entries))
	for _, e := range entries {
		d := DetectedURL{URL: e.URL, AlbumURL: e.AlbumURL}
		if site, err := app.DetectSite(e.URL); err != nil {
			d.Error = err.Error()
		} else {
			d.Site = site
		}
		out = append(out, d)
	}
	return out
}

type plan struct {
	entries []app.Entry
	single  bool
	site    string
}

func parseInput(in RunInput) (plan, error) {
	entries, err := app.ParseURLList(strings.NewReader(in.Text))
	if err != nil {
		return plan{}, err
	}
	if len(entries) == 0 {
		return plan{}, errors.New("no URLs provided")
	}
	p := plan{entries: entries, single: len(entries) == 1 && !in.FromFile}
	if in.Site != "" {
		if !p.single {
			return plan{}, errors.New("a site can only be chosen for a single URL")
		}
		p.site = in.Site
		return p, nil
	}
	for _, e := range entries {
		site, err := app.DetectSite(e.URL)
		if err != nil {
			return plan{}, err
		}
		if p.single {
			p.site = site
		}
	}
	return p, nil
}

// --- Runs ---

// StartRun downloads the input with the current settings.
func (a *App) StartRun(in RunInput) error {
	return a.startRun(in, false, nil)
}

func (a *App) startRun(in RunInput, auto bool, onEnd func(error)) error {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	p, err := parseInput(in)
	if err != nil {
		return err
	}
	if r := a.current(); r != nil && !r.ended() {
		return errors.New("a run is already in progress")
	}
	cfg, err := a.runConfig()
	if err != nil {
		return err
	}
	r := &runner{app: a, cfg: cfg, batch: !p.single, auto: auto, onEnd: onEnd}
	if cfg.LogFile != "" {
		if r.logFile, err = app.OpenLogFile(cfg.LogFile); err != nil {
			return err
		}
	}
	r.hooks = app.Hooks{
		Notice: func(msg string) { r.logf("%s", msg); a.notice(msg) },
		Log:    func(msg string) { r.logf("%s", msg) },
	}
	if r.groups, err = a.buildGroups(cfg, p, r.hooks); err != nil {
		r.closeLog()
		return err
	}
	r.start()
	a.mu.Lock()
	a.run = r
	a.mu.Unlock()
	return nil
}

func (a *App) buildGroups(cfg *config.Config, p plan, hooks app.Hooks) ([]group, error) {
	cookies := cfg.ParsedCookies()
	if p.single {
		url, site := p.entries[0].URL, p.site
		return []group{{build: func() (*session.Session, error) {
			scraper, err := a.newScraper(site, cfg.Proxy, cookies)
			if err != nil {
				return nil, err
			}
			dl, err := a.newDownloader(scraper, site, cfg, hooks)
			if err != nil {
				return nil, err
			}
			filtered := app.NewFilterScraper(scraper, cfg, 0, cfg.DiskCheck, hooks)
			s := session.New(func(ctx context.Context) ([]model.MediaItem, error) {
				return filtered.Parse(ctx, url)
			}, dl, cfg.OutputDir, site)
			s.AlbumSlug = app.AlbumSlug(site, url)
			s.SourceURL = url
			return s, nil
		}}}, nil
	}

	reg, err := a.registry(cfg.Proxy, cookies)
	if err != nil {
		return nil, err
	}
	ytEntries, otherEntries, err := app.SplitGroups(p.entries)
	if err != nil {
		return nil, err
	}
	var groups []group
	if len(ytEntries) > 0 {
		groups = append(groups, group{build: func() (*session.Session, error) {
			yt := a.ytScraper()
			dl, err := a.newDownloader(yt, "youtube", cfg, hooks)
			if err != nil {
				return nil, fmt.Errorf("youtube downloader: %w", err)
			}
			return a.batchSession(cfg, reg, ytEntries, yt, dl, hooks)
		}})
	}
	if len(otherEntries) > 0 {
		groups = append(groups, group{build: func() (*session.Session, error) {
			dl, err := a.newDownloader(nil, "generic", cfg, hooks)
			if err != nil {
				return nil, fmt.Errorf("downloader: %w", err)
			}
			return a.batchSession(cfg, reg, otherEntries, nil, dl, hooks)
		}})
	}
	return groups, nil
}

func (a *App) batchSession(cfg *config.Config, reg map[string]model.Scraper, entries []app.Entry, yt model.Scraper, dl model.Downloader, hooks app.Hooks) (*session.Session, error) {
	albums, err := app.BuildAlbums(cfg, reg, entries, yt, hooks)
	if err != nil {
		dl.Shutdown()
		return nil, err
	}
	return session.NewBatch(dl, cfg.OutputDir, albums), nil
}

// StopRun cancels every download, writes the retry list and ends the run.
func (a *App) StopRun() {
	if r := a.current(); r != nil {
		r.cancel()
		<-r.done
	}
}

// ContinueRun closes the finished group (writing its retry list) and
// starts the next one, like quitting a TUI summary.
func (a *App) ContinueRun() {
	a.with(func(r *runner) {
		if !r.finished {
			r.finishGroup()
		}
	})
}

// RunState returns the current run summary.
func (a *App) RunState() RunState {
	var st RunState
	if !a.with(func(r *runner) { st = r.state() }) {
		return RunState{}
	}
	a.mu.Lock()
	if w := a.watch; w != nil {
		st.Watching = true
		if !w.next.IsZero() {
			st.NextRun = w.next.Format(time.RFC3339)
		}
	}
	a.mu.Unlock()
	return st
}

// Rows returns visible rows [offset, offset+limit).
func (a *App) Rows(offset, limit int) []RowView {
	var out []RowView
	a.withSession(func(r *runner, s *session.Session) {
		for n, row := range s.Rows(s.Visible(), offset, offset+limit) {
			out = append(out, rowView(offset+n, row))
		}
	})
	return out
}

// with runs f against the current run on its loop; false when there is
// no run.
func (a *App) with(f func(r *runner)) bool {
	r := a.current()
	if r == nil {
		return false
	}
	r.call(func() { f(r) })
	return true
}

func (a *App) withSession(f func(r *runner, s *session.Session)) {
	a.with(func(r *runner) {
		if r.sess != nil {
			f(r, r.sess)
		}
	})
}

func (a *App) mutate(f func(r *runner, s *session.Session)) {
	a.withSession(func(r *runner, s *session.Session) {
		f(r, s)
		r.bump()
	})
}

// CancelItem cancels item index if it is pending or downloading.
func (a *App) CancelItem(index int) {
	a.mutate(func(_ *runner, s *session.Session) { s.Cancel(index) })
}

// CancelAll cancels every pending or downloading item.
func (a *App) CancelAll() {
	a.mutate(func(_ *runner, s *session.Session) { s.CancelAll() })
}

// RetryItem restarts failed item index.
func (a *App) RetryItem(index int) {
	a.mutate(func(r *runner, s *session.Session) { r.spawn(s, s.Retry(index)) })
}

// RetryAllFailed restarts every failed item.
func (a *App) RetryAllFailed() {
	a.mutate(func(r *runner, s *session.Session) { r.spawn(s, s.RetryAllFailed()) })
}

// ToggleFold folds or unfolds the album containing item index.
func (a *App) ToggleFold(index int) {
	a.mutate(func(_ *runner, s *session.Session) { s.ToggleFold(index) })
}

// SetFilter shows all, failed, active or pending items.
func (a *App) SetFilter(name string) error {
	f, ok := filterNames[name]
	if !ok {
		return fmt.Errorf("unknown filter %q", name)
	}
	a.mutate(func(_ *runner, s *session.Session) { s.Filter = f })
	return nil
}

// DismissSummary leaves the summary for the item list.
func (a *App) DismissSummary() {
	a.mutate(func(_ *runner, s *session.Session) { s.DismissSummary() })
}

// FindNext returns the next visible item after fromIndex matching query
// (or the next failed item when failed is set), wrapping around; Row is
// -1 when nothing matches.
func (a *App) FindNext(query string, failed bool, fromIndex int) Match {
	m := Match{Row: -1, Index: -1}
	a.withSession(func(_ *runner, s *session.Session) {
		visible := s.Visible()
		start := -1
		for row, idx := range visible {
			if idx == fromIndex {
				start = row
				break
			}
		}
		match := session.MatchTitle(query)
		if failed {
			match = session.IsFailed
		}
		if row := s.FindNext(visible, start, match); row >= 0 {
			m = Match{Row: row, Index: visible[row]}
		}
	})
	return m
}

// ErrorLog returns the run's scrape errors.
func (a *App) ErrorLog() []string {
	var out []string
	a.withSession(func(_ *runner, s *session.Session) { out = append(out, s.ErrorLog...) })
	return out
}

// ItemURL returns item index's scrapable URL, without query or fragment.
func (a *App) ItemURL(index int) string {
	var u string
	a.withSession(func(_ *runner, s *session.Session) {
		if index >= 0 && index < len(s.Items) && !s.Items[index].Header {
			u = app.RetryItemURL(s.Items[index].Item)
		}
	})
	return u
}

// SaveRetryList writes the current failures to a retry list in the
// output folder and returns its path ("" when nothing failed).
func (a *App) SaveRetryList() (string, error) {
	var path string
	var err error
	a.withSession(func(r *runner, s *session.Session) {
		var rep app.RetryReport
		rep, err = app.WriteRetryReport(r.cfg.OutputDir, s.Failures(), time.Now())
		path = rep.Path
	})
	return path, err
}
