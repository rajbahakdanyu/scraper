package cyberdrop

import (
	"context"
	"net/http"
	"time"
)

func NewCyberDropScraper() *Scraper {
	s, _ := NewCyberDropScraperWithOptions("", nil)
	return s
}

type noopSleeper struct{}

func (noopSleeper) Sleep(time.Duration) {}

func (noopSleeper) SleepCtx(context.Context, time.Duration) error { return nil }

func newTestCyberDropScraper() *Scraper {
	scraper := NewCyberDropScraper()
	scraper.client = &http.Client{}
	return scraper
}
