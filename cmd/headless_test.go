package cmd

import (
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestWatchSignalsShutsDownOnInterrupt(t *testing.T) {
	dl := newFakePlainDownloader()
	done, stop := watchSignals(dl)
	defer stop()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("interrupt watcher did not shut down downloader")
	}
	select {
	case _, ok := <-dl.progress:
		if ok {
			t.Fatal("expected the downloader's progress channel to be closed by Shutdown")
		}
	default:
		t.Fatal("Shutdown must have closed the progress channel after the signal")
	}
}

func TestWatchSignalsStopReleasesWatcher(t *testing.T) {
	done, stop := watchSignals(newFakePlainDownloader())
	stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() must release the signal watcher goroutine")
	}
	stop() // idempotent
}

func TestNonCancelledErrs(t *testing.T) {
	in := []error{
		errors.New("real failure"),
		&model.CancelledError{URL: "https://example.com/x"},
		nil,
		errors.New("another failure"),
	}
	got := nonCancelledErrs(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 non-cancelled errors, got %d: %v", len(got), got)
	}
	if model.IsCancelled(got[0]) || model.IsCancelled(got[1]) {
		t.Errorf("cancelled error survived filtering: %v", got)
	}
}
