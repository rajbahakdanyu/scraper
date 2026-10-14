package bunker

import (
	"context"
	"net/http"
	"time"
)

func NewBunkerScraper() *Scraper {
	s, _ := NewBunkerScraperWithOptions("", nil)
	return s
}

type noopSleeper struct{}

func (noopSleeper) Sleep(time.Duration) {}

func (noopSleeper) SleepCtx(context.Context, time.Duration) error { return nil }

func newTestBunkerScraper() *Scraper {
	scraper := NewBunkerScraper()
	scraper.client = &http.Client{}
	scraper.SetSleeper(noopSleeper{})
	return scraper
}
