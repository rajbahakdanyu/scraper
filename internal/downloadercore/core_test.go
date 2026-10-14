package downloadercore

import (
	"context"
	"testing"

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
