package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/ui/dashboard"
)

// failedItem pairs a media item that did not download with the error that
// ended it.
type failedItem struct {
	Item model.MediaItem
	Err  error
}

// collectFailures turns per-item errors into the failures worth reporting:
// nil errors and user cancellations are not failures.
func collectFailures(items []model.MediaItem, errs []error) []failedItem {
	var out []failedItem
	for i, item := range items {
		if i >= len(errs) || errs[i] == nil || model.IsCancelled(errs[i]) {
			continue
		}
		out = append(out, failedItem{Item: item, Err: errs[i]})
	}
	return out
}

// retryURL picks the URL a retry run should use: the human-facing page
// when the parser recorded one, else the item's URL with any query or
// fragment stripped so signed parameters never reach the retry list.
func retryURL(item model.MediaItem) string {
	if item.PageURL != "" {
		return item.PageURL
	}
	u, err := url.Parse(item.URL)
	if err != nil {
		return ""
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// failureReason classifies an error into a short reason for the grouped
// summary.
func failureReason(err error) string {
	if err == nil {
		return "unknown error"
	}
	var hse *model.HTTPStatusError
	if errors.As(err, &hse) {
		if host := hostFromURL(hse.URL); host != "" {
			return fmt.Sprintf("HTTP %d from %s", hse.StatusCode, host)
		}
		return fmt.Sprintf("HTTP %d", hse.StatusCode)
	}
	var nre *model.NonRetryableError
	if errors.As(err, &nre) && nre.StatusCode > 0 {
		if nre.StatusCode == 404 {
			return "HTTP 404 (not found)"
		}
		return fmt.Sprintf("HTTP %d (permanent)", nre.StatusCode)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "connection dropped"
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return "timed out"
	}
	return "other error"
}

func hostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// formatFailureSummary groups failures by reason, most frequent first.
func formatFailureSummary(failures []failedItem) string {
	counts := map[string]int{}
	for _, f := range failures {
		counts[failureReason(f.Err)]++
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(i, j int) bool {
		if counts[reasons[i]] != counts[reasons[j]] {
			return counts[reasons[i]] > counts[reasons[j]]
		}
		return reasons[i] < reasons[j]
	})
	var b strings.Builder
	for i, reason := range reasons {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "  %d × %s", counts[reason], reason)
	}
	return b.String()
}

// retryReport writes a retry list of the failed items into dir and returns
// its path plus a grouped summary. With no usable failures it returns
// empty path and summary and a nil error.
func retryReport(dir string, failures []failedItem, now time.Time) (string, string, error) {
	usable := make([]failedItem, 0, len(failures))
	seen := map[string]bool{}
	for _, f := range failures {
		u := retryURL(f.Item)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		usable = append(usable, f)
	}
	if len(usable) == 0 {
		return "", "", nil
	}

	path := filepath.Join(dir, "scraper-failed-"+now.Format("20060102-150405")+".txt")
	var list strings.Builder
	fmt.Fprintf(&list, "# scraper retry list - %s - %d failed item(s)\n",
		now.Format("2006-01-02 15:04:05"), len(usable))
	for _, f := range usable {
		list.WriteString(retryURL(f.Item) + "\n")
	}

	file, err := fileutil.OpenFileNoFollow(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", "", fmt.Errorf("write retry list: %w", err)
	}
	if _, err := io.WriteString(file, list.String()); err != nil {
		_ = file.Close()
		return "", "", fmt.Errorf("write retry list: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", "", fmt.Errorf("write retry list: %w", err)
	}

	summary := fmt.Sprintf("Failed %d item(s):\n%s\nRetry list: %s",
		len(usable), formatFailureSummary(usable), path)
	return path, summary, nil
}

// failuresFromDashboard converts the TUI's final failure list.
func failuresFromDashboard(fs []dashboard.FailedItem) []failedItem {
	out := make([]failedItem, 0, len(fs))
	for _, f := range fs {
		out = append(out, failedItem{Item: f.Item, Err: f.Err})
	}
	return out
}
