package netutil

import (
	"testing"
	"time"
)

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
			got := BackoffWithJitter(tt.base, tt.attempt)
			if got < expected || got > expected+expected/3 {
				t.Fatalf("BackoffWithJitter(%s, %d) = %s, want between %s and %s", tt.base, tt.attempt, got, expected, expected+expected/3)
			}
		})
	}
}

func TestBackoffWithJitterGrowsWithAttempt(t *testing.T) {
	const base = 500 * time.Millisecond

	previous := BackoffWithJitter(base, 0)
	for attempt := 1; attempt <= 5; attempt++ {
		got := BackoffWithJitter(base, attempt)
		if got <= previous {
			t.Fatalf("BackoffWithJitter(%s, %d) = %s, want greater than previous delay %s", base, attempt, got, previous)
		}
		previous = got
	}
}
