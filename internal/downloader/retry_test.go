package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

func TestDownloadLocalWriteFailureIsNotRetried(t *testing.T) {
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write(make([]byte, 100))
	}))
	defer ts.Close()

	dest := t.TempDir()
	if err := os.Chmod(dest, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dest, 0700) })

	_, err := newTestDownloader(1, 0, "").Download(model.MediaItem{URL: ts.URL + "/a.bin", Title: "a.bin"}, dest)
	if err == nil {
		t.Fatal("expected a write failure in a read-only directory")
	}
	if !strings.Contains(err.Error(), filepath.Join(dest, "a.bin.part")) {
		t.Errorf("error must name the file it could not open: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("a local write failure must not be retried, got %d requests", got)
	}
}

func TestDownloadRetry(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "retry_test"}

	_, err := dl.Download(item, t.TempDir())
	if err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestDownloadAllFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "fail_test"}

	_, err := dl.Download(item, t.TempDir())
	if err == nil {
		t.Fatal("expected error for failing download")
	}
}

func TestDownloadOnceTimesOutWaitingForResponseHeaders(t *testing.T) {
	started := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer ts.Close()

	dl, err := NewDownloaderWithConfig(1, 50*time.Millisecond, "", "", nil)
	if err != nil {
		t.Fatalf("NewDownloaderWithConfig failed: %v", err)
	}
	client, err := netutil.NewPublicStreamingClientWithConcurrency(1, 50*time.Millisecond, ts.URL)
	if err != nil {
		t.Fatalf("NewPublicStreamingClientWithConcurrency failed: %v", err)
	}
	dl.client = client
	item := model.MediaItem{URL: ts.URL, Title: "headers.bin"}

	start := time.Now()
	_, err = dl.downloadOnce(context.Background(), item, item.URL, filepath.Join(t.TempDir(), "headers.bin.part"))
	<-started
	if err == nil {
		t.Fatal("expected response-header timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("response-header timeout took %v", elapsed)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "5", 5 * time.Second},
		{"zero", "0", 0},
		{"negative", "-3", 0},
		{"http date", now.Add(10 * time.Second).Format(http.TimeFormat), 10 * time.Second},
		{"past date", now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"garbage", "soon", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseRetryAfter(tt.header, now); got != tt.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", tt.header, got, tt.want)
			}
		})
	}
}

type recordingSleeper struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (s *recordingSleeper) Sleep(time.Duration) {}

func (s *recordingSleeper) SleepCtx(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delays = append(s.delays, d)
	return nil
}

// A real 503 with Retry-After must flow through downloadOnce into the
// sleeper, and a huge hint must still be capped at netutil.MaxRetryDelay.
func TestDownloadHonorsRetryAfterFromResponse(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter string
		wantMin    time.Duration
	}{
		{"seconds", "2", 2 * time.Second},
		{"huge capped", "1000000000", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if atomic.AddInt32(&requests, 1) == 1 {
					w.Header().Set("Retry-After", tt.retryAfter)
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.Write([]byte("ok"))
			}))
			defer ts.Close()

			dl := newTestDownloader(1, 0, "")
			sleeper := &recordingSleeper{}
			dl.SetSleeper(sleeper)
			item := model.MediaItem{URL: ts.URL + "/file", Title: "retryafter.bin"}
			if _, err := dl.Download(item, t.TempDir()); err != nil {
				t.Fatalf("expected retry to succeed, got %v", err)
			}
			if len(sleeper.delays) != 1 {
				t.Fatalf("delays = %v, want exactly one", sleeper.delays)
			}
			if tt.wantMin > 0 && sleeper.delays[0] < tt.wantMin {
				t.Fatalf("delay = %s, want >= %s", sleeper.delays[0], tt.wantMin)
			}
			if sleeper.delays[0] > netutil.MaxRetryDelay {
				t.Fatalf("delay = %s, want <= %s", sleeper.delays[0], netutil.MaxRetryDelay)
			}
		})
	}
}
