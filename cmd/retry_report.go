package cmd

import (
	"time"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

// retryReport writes a retry list of the failed items into dir and returns
// its path plus a grouped summary. With no usable failures it returns
// empty path and summary and a nil error.
func retryReport(dir string, failures []model.FailedItem, now time.Time) (string, string, error) {
	r, err := app.WriteRetryReport(dir, failures, now)
	if err != nil || r.Path == "" {
		return "", "", err
	}
	logLine("retry list: %s (%d failed item(s), %d line(s))", r.Path, r.Failed, r.Lines)
	return r.Path, r.Summary, nil
}
