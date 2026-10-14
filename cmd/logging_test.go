package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestLogFileWritesRunLines(t *testing.T) {
	prev := logFile
	t.Cleanup(func() { logFile = prev })

	path := filepath.Join(t.TempDir(), "run.log")
	if err := openLogFile(path); err != nil {
		t.Fatal(err)
	}
	logLine("hello %s", "world")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello world") {
		t.Fatalf("log = %q, want the logged line", data)
	}
}

func TestWithBufferedNoticesHoldsWarningsUntilTheTUIExits(t *testing.T) {
	var during string
	after := captureStderr(func() {
		_ = withBufferedNotices(func() error {
			during = captureStderr(func() {
				applyAlbumItemCap(make([]model.MediaItem, 3), 1)
			})
			return nil
		})
	})
	if during != "" {
		t.Fatalf("notices must not reach stderr while the TUI runs, got %q", during)
	}
	if !strings.Contains(after, "album item cap") {
		t.Fatalf("held notices must be printed after the TUI exits, got %q", after)
	}
}

func TestOpenLogFileClosesPreviousFile(t *testing.T) {
	orig := logFile
	t.Cleanup(func() { logFile = orig })
	dir := t.TempDir()
	if err := openLogFile(filepath.Join(dir, "a.log")); err != nil {
		t.Fatal(err)
	}
	first := logFile.(*os.File)
	if err := openLogFile(filepath.Join(dir, "b.log")); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("x")); err == nil {
		t.Fatal("reopening the log must close the previous file")
	}
	_ = logFile.(*os.File).Close()
}
