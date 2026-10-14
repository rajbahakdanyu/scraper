package goonbox

import (
	"context"
	"net/http"
	"time"
)

func NewGoonBoxScraper() *Scraper {
	s, _ := NewGoonBoxScraperWithOptions("", nil)
	return s
}

type noopSleeper struct{}

func (noopSleeper) Sleep(time.Duration) {}

func (noopSleeper) SleepCtx(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func newTestGoonBoxScraper() *Scraper {
	scraper := NewGoonBoxScraper()
	scraper.client = &http.Client{}
	scraper.SetSleeper(noopSleeper{})
	return scraper
}
