package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/notify"
)

// logFile is the optional --log-file sink; nil when unset.
var logFile io.Writer

// noticeSink, when non-nil, receives in-run warnings instead of stderr.
var (
	noticeMu   sync.Mutex
	noticeSink io.Writer
)

// notice prints an in-run warning (filters, caps, disk space). Scrapes run
// while the TUI owns the terminal, so these must not go straight to stderr.
func notice(format string, args ...any) {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	w := noticeSink
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format, args...)
}

// withBufferedNotices holds notices while fn owns the terminal and prints
// them once it returns.
func withBufferedNotices(fn func() error) error {
	var buf bytes.Buffer
	noticeMu.Lock()
	noticeSink = &buf
	noticeMu.Unlock()

	err := fn()

	noticeMu.Lock()
	noticeSink = nil
	held := buf.String()
	noticeMu.Unlock()
	fmt.Fprint(os.Stderr, held)
	return err
}

// openLogFile opens (appending) the optional run log. A failure is fatal so
// a typo'd path is caught before any downloads start.
func openLogFile(path string) error {
	expanded, err := config.ExpandHomePath(path)
	if err != nil {
		return err
	}
	f, err := fileutil.OpenFileNoFollow(expanded, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if previous, ok := logFile.(io.Closer); ok {
		_ = previous.Close()
	}
	logFile = f
	return nil
}

// watchLoop runs the list once, then re-runs it every interval until
// interrupted. Run failures are reported and the loop keeps watching.
func watchLoop(runOnce func() error, interval time.Duration) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	for {
		if err := runOnce(); err != nil {
			fmt.Fprintf(os.Stderr, "watch: %v\n", err)
			logLine("watch error: %v", err)
		}
		fmt.Fprintf(os.Stderr, "watch: next run in %s (Ctrl+C to stop)\n", interval)
		select {
		case <-signals:
			return nil
		case <-time.After(interval):
		}
	}
}

// logLine writes one timestamped line to the optional run log.
func logLine(format string, args ...interface{}) {
	if logFile == nil {
		return
	}
	fmt.Fprintf(logFile, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// notificationSummary maps a run's terminal error to the notification text.
func notificationSummary(err error) string {
	if err != nil {
		return "Download failed"
	}
	return "Download complete"
}

// notifySend is a test seam; production always sends through notify.Send.
var notifySend = notify.Send

func tryNotify(summary string) {
	if err := notifySend(summary); err != nil {
		fmt.Fprintf(os.Stderr, "notification: %v\n", err)
	}
}
