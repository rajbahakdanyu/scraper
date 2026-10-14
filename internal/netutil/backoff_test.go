package netutil_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

type recordingSleeper struct {
	delays []time.Duration
	err    error
}

func (s *recordingSleeper) SleepCtx(_ context.Context, delay time.Duration) error {
	s.delays = append(s.delays, delay)
	return s.err
}

func (s *recordingSleeper) Sleep(time.Duration) {}

func TestRetryRetriesUntilSuccess(t *testing.T) {
	sleeper := &recordingSleeper{}
	attempts := 0
	err := netutil.Retry(context.Background(), 3, time.Millisecond, sleeper, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("Retry() error = %v, attempts = %d", err, attempts)
	}
	if len(sleeper.delays) != 2 {
		t.Fatalf("sleep count = %d, want 2", len(sleeper.delays))
	}
}

func TestRetryStopsOnNonRetryableError(t *testing.T) {
	sleeper := &recordingSleeper{}
	attempts := 0
	want := errors.New("permanent")
	err := netutil.Retry(context.Background(), 3, time.Millisecond, sleeper, func() error {
		attempts++
		return model.NewNonRetryable(want)
	})
	if !errors.Is(err, want) || attempts != 1 || len(sleeper.delays) != 0 {
		t.Fatalf("Retry() error = %v, attempts = %d, sleeps = %d", err, attempts, len(sleeper.delays))
	}
}

func TestRetryGivesUpAfterMaxAttempts(t *testing.T) {
	sleeper := &recordingSleeper{}
	attempts := 0
	want := errors.New("always failing")
	err := netutil.Retry(context.Background(), netutil.MaxRetries, time.Millisecond, sleeper, func() error {
		attempts++
		return want
	})
	if !errors.Is(err, want) || attempts != netutil.MaxRetries {
		t.Fatalf("Retry() error = %v, attempts = %d, want %d", err, attempts, netutil.MaxRetries)
	}
}

func TestRetryStopsWhenContextCancelledDuringAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := netutil.Retry(ctx, netutil.MaxRetries, time.Millisecond, netutil.RealSleeper{}, func() error {
		attempts++
		cancel()
		return errors.New("cancelled mid-stream")
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("Retry() error = %v, attempts = %d, want context.Canceled after 1", err, attempts)
	}
}

func TestRetryReturnsSleeperCancellation(t *testing.T) {
	want := context.Canceled
	sleeper := &recordingSleeper{err: want}
	attempts := 0
	err := netutil.Retry(context.Background(), 2, time.Millisecond, sleeper, func() error {
		attempts++
		return errors.New("temporary")
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("Retry() error = %v, attempts = %d", err, attempts)
	}
}

func TestBackoffWithJitterStaysWithinBounds(t *testing.T) {
	tests := []struct {
		name    string
		base    time.Duration
		attempt int
	}{
		{name: "first attempt", base: 500 * time.Millisecond, attempt: 0},
		{name: "second attempt", base: 500 * time.Millisecond, attempt: 1},
		{name: "third attempt", base: time.Second, attempt: 2},
		{name: "later attempt", base: 250 * time.Millisecond, attempt: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expected := tt.base << uint(tt.attempt)
			got := netutil.BackoffWithJitter(tt.base, tt.attempt)
			if got < expected || got > expected+expected/3 {
				t.Fatalf("BackoffWithJitter(%s, %d) = %s, want between %s and %s", tt.base, tt.attempt, got, expected, expected+expected/3)
			}
		})
	}
}

func TestBackoffWithJitterGrowsWithAttempt(t *testing.T) {
	const base = 500 * time.Millisecond

	previous := netutil.BackoffWithJitter(base, 0)
	for attempt := 1; attempt <= 5; attempt++ {
		got := netutil.BackoffWithJitter(base, attempt)
		if got <= previous {
			t.Fatalf("BackoffWithJitter(%s, %d) = %s, want greater than previous delay %s", base, attempt, got, previous)
		}
		previous = got
	}
}

type retryAfterErr struct{ delay time.Duration }

func (e retryAfterErr) Error() string             { return "retry after" }
func (e retryAfterErr) RetryAfter() time.Duration { return e.delay }

func TestRetryHonorsRetryAfterHint(t *testing.T) {
	sleeper := &recordingSleeper{}
	attempts := 0
	err := netutil.Retry(context.Background(), 3, time.Millisecond, sleeper, func() error {
		attempts++
		if attempts < 2 {
			return retryAfterErr{delay: 2 * time.Second}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if len(sleeper.delays) != 1 || sleeper.delays[0] < 2*time.Second {
		t.Fatalf("delays = %v, want a single delay >= 2s", sleeper.delays)
	}
}

func TestRetryCapsRetryAfterHint(t *testing.T) {
	sleeper := &recordingSleeper{}
	attempts := 0
	err := netutil.Retry(context.Background(), 2, time.Millisecond, sleeper, func() error {
		attempts++
		if attempts < 2 {
			return retryAfterErr{delay: time.Hour}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if len(sleeper.delays) != 1 || sleeper.delays[0] > netutil.MaxRetryDelay {
		t.Fatalf("delays = %v, want one delay <= %s", sleeper.delays, netutil.MaxRetryDelay)
	}
}
