package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

type fakeHost struct {
	mu      sync.Mutex
	events  []string
	data    []any
	confirm bool
}

func (h *fakeHost) Emit(event string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
	h.data = append(h.data, data)
}

func (h *fakeHost) PickDirectory(string, string) (string, error)  { return "", nil }
func (h *fakeHost) PickFile(string, []FileFilter) (string, error) { return "", nil }
func (h *fakeHost) PickSaveFile(string, string) (string, error)   { return "", nil }
func (h *fakeHost) Confirm(string, string) bool                   { return h.confirm }

func (h *fakeHost) all(event string) []any {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []any
	for i, e := range h.events {
		if e == event {
			out = append(out, h.data[i])
		}
	}
	return out
}

type fakeScraper struct {
	name  string
	items []model.MediaItem
}

func (f fakeScraper) Name() string { return f.name }

func (f fakeScraper) Parse(context.Context, string) ([]model.MediaItem, error) {
	return append([]model.MediaItem(nil), f.items...), nil
}

type fakeDownloader struct {
	mu       sync.Mutex
	ch       chan model.ProgressEvent
	failures map[string]error
	once     sync.Once
}

func (f *fakeDownloader) Prepare(model.MediaItem) {}

func (f *fakeDownloader) Download(item model.MediaItem, dest string) (string, error) {
	f.mu.Lock()
	err := f.failures[item.URL]
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	return filepath.Join(dest, model.TitleOf(item)), nil
}

func (f *fakeDownloader) Cancel(model.MediaItem) {}

func (f *fakeDownloader) Shutdown() { f.once.Do(func() { close(f.ch) }) }

func (f *fakeDownloader) Progress() <-chan model.ProgressEvent { return f.ch }

func (f *fakeDownloader) heal() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = nil
}

func media(urls ...string) []model.MediaItem {
	var out []model.MediaItem
	for _, u := range urls {
		out = append(out, model.MediaItem{URL: u, PageURL: u, Title: filepath.Base(u), Size: 10})
	}
	return out
}

// newTestApp wires an App to fakes: every site scrapes items, and the
// downloader fails the URLs in failures.
func newTestApp(t *testing.T, items []model.MediaItem, failures map[string]error) (*App, *fakeHost, *[]*fakeDownloader) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "scraper.yaml")
	h := &fakeHost{}
	a := New(cfgPath, h)
	s := a.GetSettings()
	s.OutputDir = t.TempDir()
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	var dls []*fakeDownloader
	a.newScraper = func(site, _ string, _ []*http.Cookie) (model.Scraper, error) {
		return fakeScraper{name: site, items: items}, nil
	}
	a.registry = func(string, []*http.Cookie) (map[string]model.Scraper, error) {
		reg := map[string]model.Scraper{}
		for _, site := range app.Sites() {
			reg[site] = fakeScraper{name: site, items: items}
		}
		return reg, nil
	}
	a.ytScraper = func() model.Scraper { return fakeScraper{name: "youtube", items: items} }
	a.newDownloader = func(model.Scraper, string, *config.Config, app.Hooks) (model.Downloader, error) {
		dl := &fakeDownloader{ch: make(chan model.ProgressEvent, 8), failures: failures}
		dls = append(dls, dl)
		return dl, nil
	}
	t.Cleanup(a.stopEverything)
	return a, h, &dls
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func phaseIs(a *App, phase string) func() bool {
	return func() bool { return a.RunState().Phase == phase }
}

func TestSingleRunRetryAndFinish(t *testing.T) {
	items := media("https://cyberdrop.cr/f/a.mp4", "https://cyberdrop.cr/f/b.mp4")
	a, h, dls := newTestApp(t, items, map[string]error{"https://cyberdrop.cr/f/b.mp4": errors.New("boom")})

	if err := a.StartRun(RunInput{Text: "https://cyberdrop.cr/a/album"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "summary", phaseIs(a, "summary"))

	st := a.RunState()
	if st.Stats.Completed != 1 || st.Stats.Failed != 1 || !strings.HasSuffix(st.Dest, "album") {
		t.Fatalf("state = %+v", st)
	}
	rows := a.Rows(0, 10)
	if len(rows) != 2 || rows[1].Status != "failed" || rows[1].Error != "boom" || !rows[0].HasPath {
		t.Fatalf("rows = %+v", rows)
	}
	if m := a.FindNext("", true, rows[0].Index); m.Index != rows[1].Index {
		t.Fatalf("FindNext(failed) = %+v", m)
	}
	if got := a.ItemURL(rows[1].Index); got != "https://cyberdrop.cr/f/b.mp4" {
		t.Fatalf("ItemURL = %q", got)
	}

	(*dls)[0].heal()
	a.RetryItem(rows[1].Index)
	waitFor(t, "retry to settle", func() bool {
		st := a.RunState()
		return st.Phase == "summary" && st.Stats.Failed == 0
	})

	a.ContinueRun()
	waitFor(t, "run end", func() bool { return len(h.all("run:finished")) == 1 })
	if res := h.all("run:finished")[0].(RunResult); res.Error != "" {
		t.Fatalf("finished with %+v", res)
	}
	if st := a.RunState(); st.Running {
		t.Fatalf("run still running: %+v", st)
	}
	if len(a.Rows(0, 10)) != 2 {
		t.Fatal("a finished run's list must stay viewable")
	}
}

func TestBatchRunsYouTubeGroupFirstAndWritesRetryList(t *testing.T) {
	items := media("https://x.example/f/a.mp4")
	a, h, _ := newTestApp(t, items, map[string]error{"https://x.example/f/a.mp4": errors.New("boom")})

	err := a.StartRun(RunInput{Text: "https://cyberdrop.cr/a/one\n# comment\nhttps://www.youtube.com/watch?v=abc"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "youtube summary", phaseIs(a, "summary"))
	if st := a.RunState(); st.Site != "youtube" || st.Groups != 2 || st.Group != 0 {
		t.Fatalf("first group = %+v", st)
	}
	a.ContinueRun()
	waitFor(t, "second group", func() bool {
		st := a.RunState()
		return st.Group == 1 && st.Phase == "summary"
	})
	if st := a.RunState(); st.Site != "cyberdrop" {
		t.Fatalf("second group site = %q", st.Site)
	}
	a.ContinueRun()
	waitFor(t, "run end", func() bool { return len(h.all("run:finished")) == 1 })

	groups := h.all("run:group")
	if len(groups) != 2 {
		t.Fatalf("group events = %+v", groups)
	}
	retry := groups[0].(GroupResult).RetryFile
	if retry == "" {
		t.Fatalf("expected a retry list for the failed group: %+v", groups[0])
	}
	if _, err := os.Stat(retry); err != nil {
		t.Fatal(err)
	}
	if res := h.all("run:finished")[0].(RunResult); !res.Partial {
		t.Fatalf("a batch with failures must end partial: %+v", res)
	}
}

func TestStopRunCancelsAndEnds(t *testing.T) {
	a, h, _ := newTestApp(t, media("https://cyberdrop.cr/f/a.mp4"), nil)
	if err := a.StartRun(RunInput{Text: "https://cyberdrop.cr/a/x"}); err != nil {
		t.Fatal(err)
	}
	a.StopRun()
	if len(h.all("run:finished")) != 1 {
		t.Fatal("StopRun must end the run")
	}
	if err := a.StartRun(RunInput{Text: "https://cyberdrop.cr/a/x"}); err != nil {
		t.Fatalf("a new run must start after stopping: %v", err)
	}
}

func TestParseInput(t *testing.T) {
	if _, err := parseInput(RunInput{Text: "  \n# only a comment"}); err == nil {
		t.Fatal("empty input must fail")
	}
	if _, err := parseInput(RunInput{Text: "https://a.example/x\nhttps://b.example/y", Site: "bunker"}); err == nil {
		t.Fatal("a forced site needs a single URL")
	}
	p, err := parseInput(RunInput{Text: "https://example.com/a/x", Site: "bunker"})
	if err != nil || !p.single || p.site != "bunker" {
		t.Fatalf("forced site: %+v %v", p, err)
	}
	p, err = parseInput(RunInput{Text: "https://bunkr.cr/a/x", FromFile: true})
	if err != nil || p.single {
		t.Fatalf("an imported list always runs as a batch: %+v %v", p, err)
	}
	if _, err := parseInput(RunInput{Text: "https://unknown.example/x"}); err == nil {
		t.Fatal("an unsupported site must fail")
	}
}

func TestSettingsSaveAndValidate(t *testing.T) {
	a, h, _ := newTestApp(t, nil, nil)
	if n := len(h.all("notice")); n != 0 {
		t.Fatalf("a missing config file must silently mean defaults, got %d notices", n)
	}
	s := a.GetSettings()
	s.Concurrency = 0
	if err := a.ApplySettings(s); err == nil {
		t.Fatal("concurrency 0 must be rejected")
	}
	s.Concurrency = 5
	s.Quality, s.AudioOnly = "720p", "m4a"
	if err := a.ApplySettings(s); err == nil {
		t.Fatal("quality and audio-only together must be rejected")
	}
	s.AudioOnly = ""
	s.ScrapeDelay = "soon"
	if err := a.ApplySettings(s); err == nil {
		t.Fatal("a bad duration must be rejected")
	}
	s.ScrapeDelay = "250ms"
	s.Include = []string{" *.mp4 ", "", "*.mp4"}
	if err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConcurrentDownloads != 5 || cfg.Quality != "720p" || cfg.ScrapeDelay != 250*time.Millisecond || len(cfg.Include) != 1 {
		t.Fatalf("saved config = %+v", cfg)
	}
	reloaded, err := a.ReloadSettings()
	if err != nil || reloaded.ScrapeDelay != "250ms" || reloaded.Concurrency != 5 {
		t.Fatalf("reloaded = %+v %v", reloaded, err)
	}
}

func TestPreviewListsItemsWithoutDownloading(t *testing.T) {
	items := append(media("https://cyberdrop.cr/f/a.mp4"), model.MediaItem{URL: "https://cyberdrop.cr/f/b.mp4", Title: "b.mp4"})
	a, _, dls := newTestApp(t, items, nil)
	albums, err := a.Preview(RunInput{Text: "https://cyberdrop.cr/a/one\nhttps://bunkr.cr/a/two"})
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 2 || len(albums[0].Items) != 2 || albums[0].TotalSize != 10 || albums[0].Unknown != 1 || albums[1].Site != "bunker" {
		t.Fatalf("albums = %+v", albums)
	}
	if len(*dls) != 0 {
		t.Fatal("preview must not build a downloader")
	}
}

func TestWatchRerunsUntilStopped(t *testing.T) {
	a, h, _ := newTestApp(t, media("https://cyberdrop.cr/f/a.mp4"), nil)
	if err := a.StartWatch(RunInput{Text: "https://cyberdrop.cr/a/x"}, "20ms"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "two watch runs", func() bool { return len(h.all("run:finished")) >= 2 })
	a.StopWatch()
	n := len(h.all("run:finished"))
	time.Sleep(60 * time.Millisecond)
	if got := len(h.all("run:finished")); got > n+1 {
		t.Fatalf("watch kept running after StopWatch: %d -> %d runs", n, got)
	}
	if err := a.StartWatch(RunInput{Text: "x"}, "nope"); err == nil {
		t.Fatal("a bad interval must be rejected")
	}
}

func TestBeforeCloseAsksWhileDownloading(t *testing.T) {
	a, h, _ := newTestApp(t, nil, nil)
	block := make(chan struct{})
	a.newScraper = func(string, string, []*http.Cookie) (model.Scraper, error) {
		return blockingScraper{block: block}, nil
	}
	if err := a.StartRun(RunInput{Text: "https://cyberdrop.cr/a/x"}); err != nil {
		t.Fatal(err)
	}
	if !a.beforeClose() {
		t.Fatal("closing over an active run must be prevented when the user says no")
	}
	h.confirm = true
	if a.beforeClose() {
		t.Fatal("confirmed close must proceed")
	}
	close(block)
	if len(h.all("run:finished")) != 1 {
		t.Fatal("closing must end the run")
	}
}

type blockingScraper struct{ block chan struct{} }

func (b blockingScraper) Name() string { return "blocking" }

func (b blockingScraper) Parse(ctx context.Context, _ string) ([]model.MediaItem, error) {
	select {
	case <-b.block:
	case <-ctx.Done():
	}
	return nil, ctx.Err()
}

func TestRevealRetryListRejectsOtherFiles(t *testing.T) {
	a, _, _ := newTestApp(t, nil, nil)
	for _, p := range []string{"relative/scraper-failed-1.txt", "/etc/passwd", "/tmp/scraper-failed-1.sh"} {
		if err := a.RevealRetryList(p); err == nil {
			t.Errorf("RevealRetryList(%q) must be rejected", p)
		}
	}
}

func stubOpenTarget(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	prev := openTarget
	openTarget = func(abs string, reveal bool) error {
		calls = append(calls, fmt.Sprintf("%s reveal=%v", abs, reveal))
		return nil
	}
	t.Cleanup(func() { openTarget = prev })
	return &calls
}

func TestOpenPathShowsFoldersAndRevealsItems(t *testing.T) {
	calls := stubOpenTarget(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "x y.mp4")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}

	if err := openPath(dir, false); err != nil {
		t.Fatal(err)
	}
	if err := openPath(file, true); err != nil {
		t.Fatal(err)
	}
	want := []string{dir + " reveal=false", file + " reveal=true"}
	if !slices.Equal(*calls, want) {
		t.Fatalf("openTarget calls = %q, want %q", *calls, want)
	}
}

func TestOpenPathRefusesToLaunchFilesOrMissingPaths(t *testing.T) {
	calls := stubOpenTarget(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}

	if err := openPath(file, false); err == nil {
		t.Error("opening (rather than revealing) a file must be refused")
	}
	if err := openPath(filepath.Join(dir, "missing"), true); err == nil {
		t.Error("a missing path must be refused")
	}
	if len(*calls) != 0 {
		t.Fatalf("refused paths reached the file manager: %q", *calls)
	}
}
