package model

import (
	"net/http"
	"time"
)

// NonRetryableError marks an error that will never succeed no matter how
// many times it's retried.
type NonRetryableError struct {
	Err        error
	StatusCode int
}

func (e *NonRetryableError) Error() string { return e.Err.Error() }
func (e *NonRetryableError) Unwrap() error { return e.Err }
func (e *NonRetryableError) NonRetryable() {}

func NewNonRetryable(err error) *NonRetryableError {
	return &NonRetryableError{Err: err}
}

func NewNonRetryableStatus(err error, code int) *NonRetryableError {
	return &NonRetryableError{Err: err, StatusCode: code}
}

func IsRetryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	default:
		return code >= 500
	}
}

// HTTPStatusError marks an HTTP response that is retryable (429 or 5xx).
// It carries the status, the redacted URL (for failure reports), and an
// optional server-supplied Retry-After delay.
type HTTPStatusError struct {
	StatusCode int
	URL        string
	Delay      time.Duration
	Err        error
}

func (e *HTTPStatusError) Error() string { return e.Err.Error() }
func (e *HTTPStatusError) Unwrap() error { return e.Err }

// RetryAfter implements the delay hint consumed by netutil.Retry.
func (e *HTTPStatusError) RetryAfter() time.Duration { return e.Delay }

func NewHTTPStatusError(err error, code int, url string, delay time.Duration) *HTTPStatusError {
	return &HTTPStatusError{Err: err, StatusCode: code, URL: url, Delay: delay}
}
