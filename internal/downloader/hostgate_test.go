package downloader

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestPerHostLimitCapsInFlightRequests(t *testing.T) {
	var inFlight, peak atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write(make([]byte, 100))
	}))
	defer ts.Close()

	dl := newTestDownloader(4, 0, "")
	dl.SetPerHostLimit(2)
	dest := t.TempDir()
	var wg sync.WaitGroup
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		wg.Go(func() {
			if _, err := dl.Download(model.MediaItem{URL: ts.URL + "/" + name + ".mp4", Title: name + ".mp4"}, dest); err != nil {
				t.Errorf("Download %s: %v", name, err)
			}
		})
	}
	wg.Wait()
	if got := peak.Load(); got != 2 {
		t.Fatalf("peak in-flight requests to one host = %d, want 2", got)
	}
}

func TestHostGateLimitIsPerHost(t *testing.T) {
	g := newHostGate(1)
	release, err := g.acquire(context.Background(), "a.example", time.Time{}, noopSleeper{})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	other, err := g.acquire(ctx, "b.example", time.Time{}, noopSleeper{})
	if err != nil {
		t.Fatalf("a busy host must not block a different host: %v", err)
	}
	other()
}

func TestHostGateAcquireHonorsCancellation(t *testing.T) {
	g := newHostGate(1)
	release, err := g.acquire(context.Background(), "a.example", time.Time{}, noopSleeper{})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.acquire(ctx, "a.example", time.Time{}, noopSleeper{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire on a full host with a cancelled ctx = %v, want context.Canceled", err)
	}
}

func TestHostGateCooldownAppliesToHostOnly(t *testing.T) {
	now := time.Unix(1000, 0)
	g := newHostGate(0)
	g.now = func() time.Time { return now }
	g.penalize("a.example", 5*time.Second)
	g.penalize("a.example", time.Second) // a shorter penalty keeps the longer cooldown
	g.penalize("b.example", time.Hour)   // capped at MaxRetryDelay

	s := &recordingSleeper{}
	for _, host := range []string{"a.example", "b.example", "c.example"} {
		release, err := g.acquire(context.Background(), host, time.Time{}, s)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	want := []time.Duration{5 * time.Second, 30 * time.Second}
	if len(s.delays) != len(want) || s.delays[0] != want[0] || s.delays[1] != want[1] {
		t.Fatalf("cooldown waits = %v, want %v", s.delays, want)
	}
}

func TestServiceUnavailableCoolsDownSiblingRequests(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write(make([]byte, 100))
	}))
	defer ts.Close()

	dl := newTestDownloader(2, 0, "")
	first := &recordingSleeper{}
	dl.SetSleeper(first)
	dest := t.TempDir()
	if _, err := dl.Download(model.MediaItem{URL: ts.URL + "/a.mp4", Title: "a.mp4"}, dest); err != nil {
		t.Fatal(err)
	}
	if len(first.delays) != 1 {
		t.Fatalf("the worker that hit the 503 must back off once, got %v", first.delays)
	}

	sibling := &recordingSleeper{}
	dl.SetSleeper(sibling)
	if _, err := dl.Download(model.MediaItem{URL: ts.URL + "/b.mp4", Title: "b.mp4"}, dest); err != nil {
		t.Fatal(err)
	}
	if len(sibling.delays) != 1 || sibling.delays[0] < 6*time.Second {
		t.Fatalf("a sibling request to the cooling host waited %v, want about 7s", sibling.delays)
	}
}
