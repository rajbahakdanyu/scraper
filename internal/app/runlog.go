package app

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/notify"
)

// OpenLogFile opens (appending) the run log at path, expanding ~.
func OpenLogFile(path string) (*os.File, error) {
	expanded, err := config.ExpandHomePath(path)
	if err != nil {
		return nil, err
	}
	f, err := fileutil.OpenFileNoFollow(expanded, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	return f, nil
}

// WriteLogLine writes one timestamped line to the run log w.
func WriteLogLine(w io.Writer, now time.Time, msg string) {
	fmt.Fprintf(w, "%s %s\n", now.Format(time.RFC3339), msg)
}

// NotificationSummary maps a run's terminal error to the notification text.
func NotificationSummary(err error) string {
	if err != nil {
		return "Download failed"
	}
	return "Download complete"
}

// Notify is the OS notification sender; a test seam.
var Notify = notify.Send
