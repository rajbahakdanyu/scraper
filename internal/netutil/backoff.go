package netutil

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

const (
	MaxRetries     = 5
	RetryBaseDelay = 1 * time.Second
	// MaxRetryDelay bounds any single retry sleep — including a
	// server-supplied Retry-After hint — so one item cannot stall a
	// batch indefinitely.
	MaxRetryDelay = 30 * time.Second
)

// retryAfterer is implemented by errors that carry a server-supplied
// retry delay (the Retry-After header).
type retryAfterer interface {
	RetryAfter() time.Duration
}

type Sleeper interface {
	Sleep(time.Duration)
	SleepCtx(context.Context, time.Duration) error
}

type RealSleeper struct{}

func (RealSleeper) Sleep(delay time.Duration) { time.Sleep(delay) }

func (RealSleeper) SleepCtx(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func Retry(ctx context.Context, attempts int, baseDelay time.Duration, sleeper Sleeper, fn func() error) error {
	if attempts <= 0 {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := BackoffWithJitter(baseDelay, attempt-1)
			var ra retryAfterer
			if errors.As(lastErr, &ra) {
				if hinted := ra.RetryAfter(); hinted > delay {
					delay = hinted
				}
			}
			if delay > MaxRetryDelay {
				delay = MaxRetryDelay
			}
			if err := sleeper.SleepCtx(ctx, delay); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		lastErr = fn()
		if lastErr == nil || isNonRetryable(lastErr) {
			return lastErr
		}
		if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
			return lastErr
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return lastErr
}

type nonRetryable interface {
	NonRetryable()
}

func isNonRetryable(err error) bool {
	var marker nonRetryable
	return errors.As(err, &marker)
}

func BackoffWithJitter(base time.Duration, attempt int) time.Duration {
	d := base << uint(attempt)
	// #nosec G404 -- retry jitter does not require cryptographic randomness.
	jitter := time.Duration(rand.Int63n(int64(d)/3 + 1))
	return d + jitter
}
