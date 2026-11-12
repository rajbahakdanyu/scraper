package downloadercore

import (
	"context"
	"runtime"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestNewFloorsConcurrency(t *testing.T) {
	if got := cap(New(0, 1, nil, false).sem); got != 1 {
		t.Fatalf("concurrency floor = %d, want 1", got)
	}
	if got := cap(New(3, 1, nil, false).sem); got != 3 {
		t.Fatalf("concurrency = %d, want 3", got)
	}
}

func TestWaitNAdmitsMoreThanBurst(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(1<<30), MinRateLimitBurst)
	if err := WaitN(context.Background(), limiter, 16*MinRateLimitBurst+1); err != nil {
		t.Fatalf("WaitN beyond burst: %v", err)
	}
	if err := WaitN(context.Background(), nil, 1<<20); err != nil {
		t.Fatalf("nil limiter must not block: %v", err)
	}
}

func TestBeginReportsQueuedCancellation(t *testing.T) {
	core := New(1, 1, nil, false)
	item := model.MediaItem{URL: "https://example.test/file"}

	core.Prepare(item.Key())
	core.Cancel(item.Key())

	_, _, _, err := core.Begin(item.Key(), item, model.ProgressEvent{Status: model.StatusCancelled})
	if _, ok := err.(*model.CancelledError); !ok {
		t.Fatalf("Begin error = %T, want *model.CancelledError", err)
	}
	select {
	case evt := <-core.Progress():
		if evt.Status != model.StatusCancelled {
			t.Fatalf("progress status = %v, want cancelled", evt.Status)
		}
	default:
		t.Fatal("Begin did not report cancellation")
	}
}

func TestBeginRejectsDuplicateKey(t *testing.T) {
	core := New(2, 4, nil, false)
	item := model.MediaItem{URL: "https://example.com"}

	_, cancel, release, err := core.Begin(item.Key(), item, model.ProgressEvent{Status: model.StatusCancelled})
	if err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	defer release()
	defer cancel()

	_, _, _, err = core.Begin(item.Key(), item, model.ProgressEvent{Status: model.StatusCancelled})
	if err == nil {
		t.Fatal("second Begin with the same key must fail, not overwrite the first")
	}
}

func TestShutdownCancelsActiveAndQueuedThenClosesProgress(t *testing.T) {
	core := New(1, 4, nil, false)
	active := model.MediaItem{URL: "https://example.test/active"}
	queued := model.MediaItem{URL: "https://example.test/queued"}
	cancelled := model.ProgressEvent{Status: model.StatusCancelled}

	ctx, _, release, err := core.Begin(active.Key(), active, cancelled)
	if err != nil {
		t.Fatal(err)
	}
	if !core.Waiting(active.Key()) || core.Waiting(queued.Key()) {
		t.Fatal("Waiting must report only begun downloads")
	}
	blocked := make(chan error, 1)
	go func() {
		_, _, _, err := core.Begin(queued.Key(), queued, cancelled)
		blocked <- err
	}()
	for !core.Waiting(queued.Key()) {
		runtime.Gosched()
	}

	core.Shutdown()
	core.Shutdown() // idempotent
	if ctx.Err() == nil {
		t.Fatal("Shutdown must cancel the active download")
	}
	if err := <-blocked; !model.IsCancelled(err) {
		t.Fatalf("download waiting for a slot: err = %v, want cancelled", err)
	}
	if _, _, _, err := core.Begin("late", model.MediaItem{URL: "late"}, cancelled); err == nil {
		t.Fatal("Begin after Shutdown must fail")
	}

	select {
	case _, open := <-drain(core.Progress()):
		if !open {
			t.Fatal("progress closed while a download was still running")
		}
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-drain(core.Progress()):
	case <-time.After(5 * time.Second):
		t.Fatal("progress not closed after the last download finished")
	}
}

// drain discards buffered events and reports when ch closes.
func drain(ch <-chan model.ProgressEvent) <-chan bool {
	done := make(chan bool, 1)
	go func() {
		for range ch {
		}
		done <- false
	}()
	return done
}

func TestSendProgressNeverBlocks(t *testing.T) {
	core := New(1, 1, nil, false)
	core.SendProgress(model.ProgressEvent{Status: model.StatusDownloading})
	core.SendProgress(model.ProgressEvent{Status: model.StatusCompleted}) // full: dropped
	if evt := <-core.Progress(); evt.Status != model.StatusDownloading {
		t.Fatalf("first event = %v", evt.Status)
	}
}

func TestLimiterAndChecksumSettings(t *testing.T) {
	limiter := rate.NewLimiter(1, 1)
	core := New(1, 1, limiter, true)
	if core.Limiter() != limiter || !core.SkipSum() {
		t.Fatal("constructor settings not kept")
	}
	core.SetLimiter(nil)
	if core.Limiter() != nil {
		t.Fatal("SetLimiter(nil) must remove the limit")
	}
}
