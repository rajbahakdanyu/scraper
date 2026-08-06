package model

import "net/http"

// NonRetryableError marks an error that will never succeed no matter how
// many times it's retried.
type NonRetryableError struct {
	Err        error
	StatusCode int
}

func (e *NonRetryableError) Error() string { return e.Err.Error() }
func (e *NonRetryableError) Unwrap() error { return e.Err }

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

func IsNonRetryable(err error) bool {
	_, ok := err.(*NonRetryableError)
	return ok
}
