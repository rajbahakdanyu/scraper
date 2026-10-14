package downloader

import (
	"fmt"
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
)

func TestDownloadCancelDuringRetryBackoff(t *testing.T) {
	requestStarted := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	sleeper := blockingSleeper{started: make(chan struct{})}
	dl.SetSleeper(sleeper)
	item := model.MediaItem{URL: ts.URL, Title: "cancel_backoff"}
	done := make(chan error, 1)
	go func() { _, err := dl.Download(item, t.TempDir()); done <- err }()

	<-requestStarted
	<-sleeper.started
	dl.Cancel(item)

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected cancellation error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt retry backoff")
	}
}

func TestCancel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	sleeper := cancelSleeper{started: make(chan struct{}), release: make(chan struct{})}
	dl.SetSleeper(sleeper)
	item := model.MediaItem{URL: ts.URL, Title: "cancel_test"}

	done := make(chan error, 1)
	go func() { _, err := dl.Download(item, t.TempDir()); done <- err }()
	<-sleeper.started
	dl.Cancel(item)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error for cancelled download")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt the retry backoff")
	}
}

func TestCancelQueuedDownload(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		if r.URL.Path == "/first" {
			close(firstStarted)
			<-releaseFirst
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	first := model.MediaItem{ID: "first", URL: ts.URL + "/first", Title: "first.bin"}
	second := model.MediaItem{ID: "second", URL: ts.URL + "/second", Title: "second.bin"}
	dest := t.TempDir()

	firstDone := make(chan error, 1)
	go func() { _, err := dl.Download(first, dest); firstDone <- err }()
	<-firstStarted

	dl.Prepare(second)
	secondDone := make(chan error, 1)
	go func() { _, err := dl.Download(second, dest); secondDone <- err }()

	deadline := time.Now().Add(time.Second)
	for !dl.Waiting(second.Key()) {
		if time.Now().After(deadline) {
			t.Fatal("second download never entered the queue")
		}
		time.Sleep(time.Millisecond)
	}

	dl.Cancel(second)
	select {
	case err := <-secondDone:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("queued download returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued cancellation did not return promptly")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("queued download reached server; requests=%d", got)
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first download failed: %v", err)
	}
}

func TestShutdownCancelsInFlightDownload(t *testing.T) {
	requestStarted := make(chan struct{})
	var once sync.Once
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(requestStarted) })
		<-r.Context().Done() // block until the client aborts
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "slow.bin"}

	downloadDone := make(chan error, 1)
	go func() { _, err := dl.Download(item, t.TempDir()); downloadDone <- err }()
	<-requestStarted

	start := time.Now()
	dl.Shutdown()

	select {
	case err := <-downloadDone:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected a cancellation error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not cancel the in-flight download")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("in-flight download took %v to abort after Shutdown", elapsed)
	}

	// Once the in-flight download has returned, the progress channel must
	// close so a consumer ranging over Progress() terminates.
	waitProgressClosed(t, dl.Progress(), 5*time.Second)
}

func TestShutdownUnblocksQueuedDownloads(t *testing.T) {
	firstStarted := make(chan struct{})
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			close(firstStarted)
		}
		<-r.Context().Done()
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	first := model.MediaItem{ID: "first", URL: ts.URL + "/first", Title: "first.bin"}
	second := model.MediaItem{ID: "second", URL: ts.URL + "/second", Title: "second.bin"}
	dest := t.TempDir()

	firstDone := make(chan error, 1)
	go func() { _, err := dl.Download(first, dest); firstDone <- err }()
	<-firstStarted

	secondDone := make(chan error, 1)
	go func() { _, err := dl.Download(second, dest); secondDone <- err }()

	// Wait until the second download is queued behind the semaphore.
	deadline := time.Now().Add(time.Second)
	for !dl.Waiting(second.Key()) {
		if time.Now().After(deadline) {
			t.Fatal("second download never entered the queue")
		}
		time.Sleep(time.Millisecond)
	}

	dl.Shutdown()
	for name, done := range map[string]chan error{"first": firstDone, "second": secondDone} {
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "cancelled") {
				t.Errorf("%s download returned %v, want a cancellation", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s download did not return after Shutdown", name)
		}
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("queued download reached the server after Shutdown; requests=%d", got)
	}
}

func TestShutdownDeliversBufferedEventsBeforeClosing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "quick.bin"}
	if _, err := dl.Download(item, t.TempDir()); err != nil {
		t.Fatalf("download failed: %v", err)
	}

	dl.Shutdown()

	// Events the download emitted before Shutdown must still be delivered
	// to a consumer that only starts ranging after Shutdown returned.
	var sawCompleted bool
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt, ok := <-dl.Progress():
			if !ok {
				if !sawCompleted {
					t.Error("progress channel closed before the completed event was delivered")
				}
				return
			}
			if evt.Status == model.StatusCompleted {
				sawCompleted = true
			}
		case <-deadline:
			t.Fatal("progress channel was not closed after Shutdown")
		}
	}
}

func TestDownloadFailsFastAfterShutdown(t *testing.T) {
	dl := newTestDownloader(1, 0, "")
	dl.Shutdown()

	// The channel closes asynchronously; the fail-fast path must never
	// send on it (a send on a closed channel would panic).
	waitProgressClosed(t, dl.Progress(), 5*time.Second)

	start := time.Now()
	_, err := dl.Download(model.MediaItem{URL: "http://example.com/x", Title: "x"}, t.TempDir())
	if err == nil {
		t.Fatal("expected Download to fail fast after Shutdown")
	}
	if !strings.Contains(err.Error(), "shut down") {
		t.Errorf("expected a shutdown error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Download blocked for %v after Shutdown", elapsed)
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	dl := newTestDownloader(1, 0, "")
	dl.Shutdown()
	dl.Shutdown() // a second call must not block or close the channel twice
	waitProgressClosed(t, dl.Progress(), 5*time.Second)
}

func TestShutdownKeepsPartialFileForResume(t *testing.T) {
	gotBytes := make(chan struct{})
	var once sync.Once
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("PARTIAL8"))
		w.(http.Flusher).Flush()
		once.Do(func() { close(gotBytes) })
		<-r.Context().Done()
	}))
	defer ts.Close()

	dl := newTestDownloader(1, 0, "")
	item := model.MediaItem{URL: ts.URL, Title: "resume_shutdown.bin"}
	dest := t.TempDir()

	downloadDone := make(chan error, 1)
	go func() { _, err := dl.Download(item, dest); downloadDone <- err }()

	// Wait until the partial bytes have actually landed on disk.
	tempPath := filepath.Join(dest, "resume_shutdown.bin.part")
	<-gotBytes
	deadline := time.Now().Add(2 * time.Second)
	for {
		if info, err := os.Stat(tempPath); err == nil && info.Size() == 8 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial bytes never landed on disk")
		}
		time.Sleep(5 * time.Millisecond)
	}

	dl.Shutdown()
	select {
	case err := <-downloadDone:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected a cancellation error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not cancel the in-flight download")
	}

	info, err := os.Stat(tempPath)
	if err != nil {
		t.Fatalf("interrupted download must keep its .part file for resume: %v", err)
	}
	if info.Size() != 8 {
		t.Errorf("expected .part to keep its 8 downloaded bytes, got %d", info.Size())
	}
}

// TestShutdownWithConcurrentDownloads hammers the sendProgress-vs-close
// race: many in-flight downloads emitting progress events while Shutdown
// cancels them and closes the channel. Run with -race; a send on a closed
// channel would panic.
func TestShutdownWithConcurrentDownloads(t *testing.T) {
	bodyStarted := make(chan struct{}, 4)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 100; i++ {
			if _, err := w.Write(make([]byte, 1024)); err != nil {
				return // client went away
			}
			if i == 0 {
				bodyStarted <- struct{}{}
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer ts.Close()

	dl := newTestDownloader(4, 0, "")
	dest := t.TempDir()
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			item := model.MediaItem{
				ID:    fmt.Sprintf("%d", i),
				URL:   ts.URL,
				Title: fmt.Sprintf("file%d.bin", i),
			}
			_, err := dl.Download(item, dest)
			errs <- err
		}(i)
	}

	// Wait until every active slot has reached the response body.
	for i := 0; i < cap(bodyStarted); i++ {
		select {
		case <-bodyStarted:
		case <-time.After(time.Second):
			t.Fatal("concurrent download did not reach the response body")
		}
	}
	dl.Shutdown()

	waitProgressClosed(t, dl.Progress(), 10*time.Second)

	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("unexpected download error after Shutdown: %v", err)
		}
	}
}
