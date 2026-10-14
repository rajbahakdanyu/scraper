package netutil

import (
	"net/http"
	"time"
)

// NewPublicClient and NewPublicClientWithProxy build the non-streaming
// public client with an explicit timeout, for tests.
func NewPublicClient(timeout time.Duration, allowedPrivateURLs ...string) (*http.Client, error) {
	return newPublicClientWithConcurrency(1, timeout, timeout, timeout, "", allowedPrivateURLs...)
}

func NewPublicClientWithProxy(timeout time.Duration, proxyURL string) (*http.Client, error) {
	return newPublicClientWithConcurrency(1, timeout, timeout, timeout, proxyURL)
}
