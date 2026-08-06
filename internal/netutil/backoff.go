package netutil

import (
	"math/rand"
	"time"
)

func BackoffWithJitter(base time.Duration, attempt int) time.Duration {
	d := base << uint(attempt)
	// #nosec G404 -- retry jitter does not require cryptographic randomness.
	jitter := time.Duration(rand.Int63n(int64(d)/3 + 1))
	return d + jitter
}
