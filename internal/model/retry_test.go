package model

import (
	"errors"
	"testing"
	"time"
)

func TestHTTPStatusErrorCarriesStatusURLAndDelay(t *testing.T) {
	inner := errors.New("HTTP 503 for https://cdn.example/file")
	err := NewHTTPStatusError(inner, 503, "https://cdn.example/file", 3*time.Second)

	if !errors.Is(err, inner) {
		t.Fatalf("HTTPStatusError does not unwrap to the inner error")
	}
	if err.StatusCode != 503 {
		t.Fatalf("StatusCode = %d, want 503", err.StatusCode)
	}
	if err.URL != "https://cdn.example/file" {
		t.Fatalf("URL = %q", err.URL)
	}
	var ra interface{ RetryAfter() time.Duration }
	if !errors.As(err, &ra) || ra.RetryAfter() != 3*time.Second {
		t.Fatalf("RetryAfter() = %v, want 3s", ra)
	}
}
