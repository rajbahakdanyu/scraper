package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

type noopSleeper struct{}

func (noopSleeper) Sleep(time.Duration)                                 {}
func (noopSleeper) SleepCtx(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func TestJSONRetriesTransientStatus(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	var got struct{ OK bool }
	if err := JSON(context.Background(), ts.Client(), ts.URL, "ua", noopSleeper{}, &got); err != nil || !got.OK {
		t.Fatalf("JSON = %+v, %v; want ok after one retry", got, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestDocDoesNotRetryPermanentStatus(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	_, err := Doc(context.Background(), ts.Client(), ts.URL, "ua", noopSleeper{})
	var nre *model.NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("a 404 must be non-retryable, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}
