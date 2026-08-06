// Package downloadercore contains the lifecycle shared by media downloaders.
package downloadercore

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/time/rate"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

type downloaderCore struct {
	mu           sync.Mutex
	wg           sync.WaitGroup
	sem          chan struct{}
	progress     chan model.ProgressEvent
	cancelMap    map[string]context.CancelFunc
	queued       map[string]struct{}
	cancelled    map[string]struct{}
	shuttingDown bool
	limiter      *rate.Limiter
	skipSum      bool
}

// Core is the cross-package name for the unexported shared implementation.
type Core = downloaderCore

func New(concurrency, progressBuffer int, limiter *rate.Limiter, skipSum bool) *Core {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Core{
		sem:       make(chan struct{}, concurrency),
		progress:  make(chan model.ProgressEvent, progressBuffer),
		cancelMap: make(map[string]context.CancelFunc),
		queued:    make(map[string]struct{}),
		cancelled: make(map[string]struct{}),
		limiter:   limiter,
		skipSum:   skipSum,
	}
}

func (c *Core) Prepare(key string) {
	c.mu.Lock()
	c.queued[key] = struct{}{}
	c.mu.Unlock()
}

func (c *Core) Cancel(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cancel, ok := c.cancelMap[key]; ok {
		cancel()
		delete(c.cancelMap, key)
	} else if _, ok := c.queued[key]; ok {
		c.cancelled[key] = struct{}{}
	}
}

func (c *Core) Shutdown() {
	c.mu.Lock()
	if c.shuttingDown {
		c.mu.Unlock()
		return
	}
	c.shuttingDown = true
	cancels := make([]context.CancelFunc, 0, len(c.cancelMap))
	for _, cancel := range c.cancelMap {
		cancels = append(cancels, cancel)
	}
	c.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	go func() {
		c.wg.Wait()
		close(c.progress)
	}()
}

func (c *Core) Progress() <-chan model.ProgressEvent { return c.progress }

func (c *Core) SendProgress(evt model.ProgressEvent) {
	select {
	case c.progress <- evt:
	default:
	}
}

func (c *Core) Begin(key string, item model.MediaItem, cancelledEvent model.ProgressEvent) (context.Context, context.CancelFunc, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	if c.shuttingDown {
		c.mu.Unlock()
		cancel()
		return nil, nil, nil, fmt.Errorf("downloader is shut down: %s", item.URL)
	}
	c.wg.Add(1)
	delete(c.queued, key)
	if _, ok := c.cancelled[key]; ok {
		delete(c.cancelled, key)
		c.mu.Unlock()
		cancel()
		cancelledEvent.Item = item
		c.SendProgress(cancelledEvent)
		c.wg.Done()
		return nil, nil, nil, &model.CancelledError{URL: item.URL}
	}
	c.cancelMap[key] = cancel
	c.mu.Unlock()

	finish := func() {
		c.mu.Lock()
		delete(c.cancelMap, key)
		c.mu.Unlock()
		cancel()
		c.wg.Done()
	}
	select {
	case c.sem <- struct{}{}:
		return ctx, cancel, func() {
			<-c.sem
			finish()
		}, nil
	case <-ctx.Done():
		cancelledEvent.Item = item
		c.SendProgress(cancelledEvent)
		finish()
		return nil, nil, nil, &model.CancelledError{URL: item.URL}
	}
}

func (c *Core) SetLimiter(limiter *rate.Limiter) { c.limiter = limiter }

func (c *Core) SetSkipSum(skip bool) { c.skipSum = skip }

func (c *Core) Limiter() *rate.Limiter { return c.limiter }

func (c *Core) SkipSum() bool { return c.skipSum }

func (c *Core) Semaphore() chan struct{} { return c.sem }

func (c *Core) Mutex() *sync.Mutex { return &c.mu }

func (c *Core) CancelMap() map[string]context.CancelFunc { return c.cancelMap }
