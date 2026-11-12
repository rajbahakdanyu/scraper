package downloader

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

// hostGate caps concurrent requests per host and shares 429/503 cooldowns
// between the workers fetching from that host, so one overloaded CDN node
// gets breathing room instead of a burst of immediate retries.
type hostGate struct {
	limit int // <= 0: unlimited requests (cooldowns still apply)
	now   func() time.Time

	mu    sync.Mutex
	hosts map[string]*hostState
}

type hostState struct {
	slots    chan struct{} // nil when unlimited
	cooldown time.Time
}

func newHostGate(limit int) *hostGate {
	return &hostGate{limit: limit, now: time.Now, hosts: make(map[string]*hostState)}
}

// hostKey is the lower-cased host name of rawURL, or "" when it has none.
func hostKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func (g *hostGate) state(host string) *hostState {
	g.mu.Lock()
	defer g.mu.Unlock()
	st, ok := g.hosts[host]
	if !ok {
		st = &hostState{}
		if g.limit > 0 {
			st.slots = make(chan struct{}, g.limit)
		}
		g.hosts[host] = st
	}
	return st
}

// acquire waits for a free request slot on host and then for any cooldown
// that host is serving, except a cooldown ending at waived: the worker that
// set it already backs off through its own retry delay. The returned
// release func must be called once the request (including its body) is
// finished.
func (g *hostGate) acquire(ctx context.Context, host string, waived time.Time, sleeper netutil.Sleeper) (func(), error) {
	st := g.state(host)
	release := func() {}
	if st.slots != nil {
		select {
		case st.slots <- struct{}{}:
			var once sync.Once
			release = func() { once.Do(func() { <-st.slots }) }
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	g.mu.Lock()
	var wait time.Duration
	if !st.cooldown.Equal(waived) {
		wait = st.cooldown.Sub(g.now())
	}
	g.mu.Unlock()
	if wait > 0 {
		if err := sleeper.SleepCtx(ctx, wait); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}

// penalize makes every worker wait delay (capped at netutil.MaxRetryDelay)
// before its next request to host. A longer active cooldown is kept. It
// returns the cooldown's end for the caller to waive on its own retry.
func (g *hostGate) penalize(host string, delay time.Duration) time.Time {
	if delay <= 0 {
		return time.Time{}
	}
	if delay > netutil.MaxRetryDelay {
		delay = netutil.MaxRetryDelay
	}
	st := g.state(host)
	g.mu.Lock()
	defer g.mu.Unlock()
	if until := g.now().Add(delay); until.After(st.cooldown) {
		st.cooldown = until
	}
	return st.cooldown
}
