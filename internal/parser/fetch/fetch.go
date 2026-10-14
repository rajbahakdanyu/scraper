// Package fetch holds the retrying page/API fetches the site scrapers share.
package fetch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

// Classify marks an HTTP status that retrying cannot fix (4xx other than
// 408/429) as non-retryable, so netutil.Retry stops after one attempt.
func Classify(err error) error {
	var statusErr *netutil.HTTPStatusError
	if errors.As(err, &statusErr) && !model.IsRetryableStatus(statusErr.StatusCode) {
		return model.NewNonRetryable(err)
	}
	return err
}

// JSON GETs url and decodes the body into target, retrying transient
// failures (5xx, 408/429, network errors) with backoff.
func JSON(ctx context.Context, client *http.Client, url, userAgent string, sleeper netutil.Sleeper, target any) error {
	return netutil.Retry(ctx, netutil.MaxRetries, netutil.RetryBaseDelay, sleeper, func() error {
		return Classify(netutil.GetJSON(ctx, client, url, userAgent, target))
	})
}

// Doc GETs an HTML page, retrying transient failures with backoff.
func Doc(ctx context.Context, client *http.Client, url, userAgent string, sleeper netutil.Sleeper) (io.Reader, error) {
	body, err := Bytes(ctx, client, url, userAgent, sleeper)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
}

// Bytes GETs a page body, retrying transient failures with backoff.
func Bytes(ctx context.Context, client *http.Client, url, userAgent string, sleeper netutil.Sleeper) ([]byte, error) {
	var body []byte
	err := netutil.Retry(ctx, netutil.MaxRetries, netutil.RetryBaseDelay, sleeper, func() error {
		var fetchErr error
		body, fetchErr = netutil.GetBytes(ctx, client, url, userAgent)
		return Classify(fetchErr)
	})
	return body, err
}
