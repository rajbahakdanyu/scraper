package filester

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

func NewFilesterScraper() *Scraper {
	s, _ := NewFilesterScraperWithOptions("", nil)
	return s
}

type noopSleeper struct{}

func (noopSleeper) Sleep(time.Duration) {}

func (noopSleeper) SleepCtx(context.Context, time.Duration) error { return nil }

type resolveSleeper struct {
	sleepCtxCalled bool
}

func (s *resolveSleeper) Sleep(time.Duration) {
	panic("resolveFile used non-context-aware sleep")
}

func (s *resolveSleeper) SleepCtx(ctx context.Context, _ time.Duration) error {
	s.sleepCtxCalled = true
	return context.Canceled
}

func newTestFilesterScraper() *Scraper {
	scraper := NewFilesterScraper()
	scraper.client = &http.Client{}
	scraper.SetSleeper(noopSleeper{})
	return scraper
}

// downloadTokenHandler returns a deterministic token payload for whatever
// file_slug is requested, optionally pointing "server" back at serverURL
// (mirroring the real /v2/api/public/download response shape).
func downloadTokenHandler(serverURL string, includeServer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			FileSlug string `json:"file_slug"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)

		resp := map[string]string{
			"file":  "file-" + body.FileSlug,
			"token": "tok-" + body.FileSlug,
			"name":  body.FileSlug + "-name.bin",
		}
		if includeServer {
			resp["server"] = serverURL
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
