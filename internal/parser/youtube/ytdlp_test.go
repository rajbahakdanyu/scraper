package youtube

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// stubYTDLP puts a fake "yt-dlp" on PATH. It records its arguments (one per
// line) and a copy of any --cookies file, reports progress for two files,
// and writes ytdlp.mp4 into its working directory.
func stubYTDLP(t *testing.T) (argsFile, cookiesCopy string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	cookiesCopy = filepath.Join(dir, "cookies")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$YTDLP_ARGS_FILE"
prev=""
for arg do
  if [ "$prev" = "--cookies" ]; then cp "$arg" "$YTDLP_COOKIES_COPY"; fi
  prev="$arg"
done
echo "SCRAPER_PROGRESS 5 10 NA ytdlp.f1.mp4"
echo "SCRAPER_PROGRESS 10 10 NA ytdlp.f1.mp4"
echo "SCRAPER_PROGRESS 4 NA 4.0 ytdlp.f2.m4a"
printf 'yt-dlp output' > ytdlp.mp4
echo "SCRAPER_FILE:ytdlp.mp4"
`
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("YTDLP_ARGS_FILE", argsFile)
	t.Setenv("YTDLP_COOKIES_COPY", cookiesCopy)
	return argsFile, cookiesCopy
}

type warnings struct {
	mu   sync.Mutex
	msgs []string
}

func (w *warnings) add(msg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, msg)
}

func (w *warnings) joined() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.msgs, "\n")
}

func newYTDLPTestDownloader(t *testing.T, fake *fakeYouTube, cfg DownloaderConfig) (*Downloader, *warnings) {
	t.Helper()
	w := &warnings{}
	cfg.Concurrency = 1
	cfg.Warn = w.add
	scraper := NewYouTubeScraper()
	d, err := NewDownloaderWithConfig(scraper, cfg)
	if err != nil {
		t.Fatal(err)
	}
	d.sleeper = instantSleeper{}
	fake.wire(scraper)
	return d, w
}

func forbidStreams(fake *fakeYouTube) {
	fake.streamHook = func(w http.ResponseWriter, _ *http.Request) bool {
		w.WriteHeader(http.StatusForbidden)
		return true
	}
}

func TestYTDLPBackendDownloadsWithSettingsAndYouTubeCookiesOnly(t *testing.T) {
	const videoID = "ytdlpvid001"
	fake := startFakeYouTube(t, videoID, "Via yt-dlp", "unused")
	argsFile, cookiesCopy := stubYTDLP(t)
	d, _ := newYTDLPTestDownloader(t, fake, DownloaderConfig{
		Backend:   BackendYTDLP,
		Quality:   "720p",
		ProxyURL:  "http://proxy.test:8080",
		RateLimit: 500000,
		Cookies: []*http.Cookie{
			{Name: "SID", Value: "yt", Domain: ".youtube.com", Path: "/"},
			{Name: "other", Value: "secret", Domain: ".bunkr.cr", Path: "/"},
		},
	})
	var mu sync.Mutex
	var progress []int64
	go func() {
		for evt := range d.Progress() {
			if evt.Status == model.StatusDownloading && evt.Bytes > 0 {
				mu.Lock()
				progress = append(progress, evt.Bytes)
				mu.Unlock()
			}
		}
	}()

	dest := t.TempDir()
	path, err := d.Download(model.MediaItem{URL: videoWatchURL(videoID), Title: "Via yt-dlp", Type: model.MediaTypeVideo}, dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertDownloadedFile(t, dest, "Via yt-dlp.mp4", "yt-dlp output")
	if filepath.Base(path) != "Via yt-dlp.mp4" {
		t.Fatalf("path = %q", path)
	}
	if fake.playerPosts.Load() != 0 {
		t.Fatal("yt-dlp backend must not use the built-in client")
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{
		"bv*[height<=720]+ba/b[height<=720]/bv*+ba/b", "--proxy\nhttp://proxy.test:8080",
		"--limit-rate\n500000", "--ignore-config", "-S\nres,vcodec:h264,acodec:aac", "--\n" + videoWatchURL(videoID),
	} {
		if !strings.Contains(string(args), want) {
			t.Errorf("yt-dlp args missing %q:\n%s", want, args)
		}
	}
	cookies, _ := os.ReadFile(cookiesCopy)
	if !strings.Contains(string(cookies), "SID\tyt") || strings.Contains(string(cookies), "secret") {
		t.Errorf("cookie file must hold only YouTube cookies:\n%s", cookies)
	}
	if dirs := workDirs(t, dest); len(dirs) != 0 {
		t.Errorf("work dirs after success = %v (cookie file must not linger)", dirs)
	}

	d.Shutdown()
	waitProgressClosed(t, d.Progress(), 5*time.Second)
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(progress); i++ {
		if progress[i] < progress[i-1] {
			t.Fatalf("progress went backwards across yt-dlp's files: %v", progress)
		}
	}
	if len(progress) == 0 || progress[len(progress)-1] != 14 {
		t.Fatalf("progress = %v, want to end at 14 (10 + 4 bytes)", progress)
	}
}

func TestAutoBackendFallsBackToYTDLPWhenStreamForbidden(t *testing.T) {
	const videoID = "fallback001"
	fake := startFakeYouTube(t, videoID, "Fallback", "content")
	forbidStreams(fake)
	argsFile, _ := stubYTDLP(t)
	d, w := newYTDLPTestDownloader(t, fake, DownloaderConfig{})

	dest := t.TempDir()
	if _, err := d.Download(model.MediaItem{URL: videoWatchURL(videoID), Title: "Fallback", Type: model.MediaTypeVideo}, dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertDownloadedFile(t, dest, "Fallback.mp4", "yt-dlp output")
	if _, err := os.Stat(argsFile); err != nil {
		t.Fatal("yt-dlp was not run")
	}
	if !strings.Contains(w.joined(), "retrying with yt-dlp") {
		t.Errorf("no fallback notice; warnings: %q", w.joined())
	}
	if dirs := workDirs(t, dest); len(dirs) != 0 {
		t.Errorf("built-in partials left after a successful fallback: %v", dirs)
	}
}

func TestBuiltinBackendNeverRunsYTDLP(t *testing.T) {
	const videoID = "builtin0001"
	fake := startFakeYouTube(t, videoID, "Builtin", "content")
	forbidStreams(fake)
	argsFile, _ := stubYTDLP(t)
	d, _ := newYTDLPTestDownloader(t, fake, DownloaderConfig{Backend: BackendBuiltin})

	_, err := d.Download(model.MediaItem{URL: videoWatchURL(videoID), Title: "Builtin", Type: model.MediaTypeVideo}, t.TempDir())
	var status *streamStatusError
	if !errors.As(err, &status) || status.Code != http.StatusForbidden {
		t.Fatalf("err = %v, want the built-in 403", err)
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Fatal("builtin backend ran yt-dlp")
	}
}

func TestAutoBackendDoesNotFallBackOnServerErrors(t *testing.T) {
	const videoID = "server50001"
	fake := startFakeYouTube(t, videoID, "Server", "content")
	fake.streamHook = func(w http.ResponseWriter, _ *http.Request) bool {
		w.WriteHeader(http.StatusServiceUnavailable)
		return true
	}
	argsFile, _ := stubYTDLP(t)
	d, _ := newYTDLPTestDownloader(t, fake, DownloaderConfig{})

	if _, err := d.Download(model.MediaItem{URL: videoWatchURL(videoID), Title: "Server", Type: model.MediaTypeVideo}, t.TempDir()); err == nil {
		t.Fatal("expected the 503 to fail the download")
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Fatal("a server outage must not trigger the yt-dlp fallback")
	}
}

func TestYTDLPBackendWithoutBinaryFailsClearly(t *testing.T) {
	const videoID = "nobinary001"
	fake := startFakeYouTube(t, videoID, "Missing", "content")
	t.Setenv("PATH", t.TempDir())
	d, _ := newYTDLPTestDownloader(t, fake, DownloaderConfig{Backend: BackendYTDLP})

	_, err := d.Download(model.MediaItem{URL: videoWatchURL(videoID), Title: "Missing", Type: model.MediaTypeVideo}, t.TempDir())
	if !errors.Is(err, errYTDLPMissing) {
		t.Fatalf("err = %v, want errYTDLPMissing", err)
	}
}
