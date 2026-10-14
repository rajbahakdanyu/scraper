package downloader

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

type noopSleeper struct{}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type cleanupTriggerBody struct {
	io.ReadCloser
	trigger func()
	once    sync.Once
}

func (b *cleanupTriggerBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.once.Do(b.trigger)
	}
	return n, err
}

func (noopSleeper) Sleep(time.Duration) {}

func (noopSleeper) SleepCtx(context.Context, time.Duration) error { return nil }

type blockingSleeper struct {
	started chan struct{}
}

type cancelSleeper struct {
	started chan struct{}
	release chan struct{}
}

func (s cancelSleeper) Sleep(time.Duration) {}

func (s cancelSleeper) SleepCtx(ctx context.Context, _ time.Duration) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s blockingSleeper) Sleep(time.Duration) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {}
}

func (s blockingSleeper) SleepCtx(ctx context.Context, _ time.Duration) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Hour):
		return nil
	}
}

func newTestDownloader(concurrency int, timeout time.Duration, userAgent string) *Downloader {
	dl, _ := NewDownloaderWithConfig(concurrency, timeout, userAgent, "", nil)
	dl.client = &http.Client{}
	dl.SetSleeper(noopSleeper{})
	return dl
}

// waitProgressClosed drains ch until it closes, failing the test if that
// takes longer than timeout.
func waitProgressClosed(t *testing.T, ch <-chan model.ProgressEvent, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("progress channel was not closed")
		}
	}
}
