package app

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
)

// CollectFailures turns per-item errors into the failures worth reporting:
// nil errors and user cancellations are not failures. albumURL is the URL
// the items were scraped from, recorded so a retry can restore the album
// folder.
func CollectFailures(items []model.MediaItem, errs []error, albumURL string) []model.FailedItem {
	var out []model.FailedItem
	for i, item := range items {
		if i >= len(errs) || errs[i] == nil || model.IsCancelled(errs[i]) {
			continue
		}
		out = append(out, model.FailedItem{Item: item, Err: errs[i], AlbumURL: albumURL})
	}
	return out
}

func CollectScrapeFailure(err error, albumURL string) []model.FailedItem {
	if err == nil || model.IsCancelled(err) || albumURL == "" {
		return nil
	}
	return []model.FailedItem{{
		Item:     model.MediaItem{URL: albumURL},
		Err:      err,
		AlbumURL: albumURL,
	}}
}

// RetryItemURL returns the item's own scrapable page URL, or its URL with
// any query or fragment stripped so signed parameters never reach the
// retry list.
func RetryItemURL(item model.MediaItem) string {
	if item.PageURL != "" {
		return stripQueryFragment(item.PageURL)
	}
	return stripQueryFragment(item.URL)
}

// RetryEntry renders one retry-list line: the item's page URL paired with
// its album when the item has its own page, else the album URL alone.
// Items whose URL is a direct CDN link (goonbox and friends) cannot be
// scraped on their own, so their album is the only replayable entry.
func RetryEntry(f model.FailedItem) string {
	itemURL := f.Item.PageURL
	if itemURL == "" {
		itemURL = f.Item.URL
	}
	if isYouTubeWatchURL(itemURL) {
		return stripQueryFragment(itemURL)
	}
	if page := stripQueryFragment(f.Item.PageURL); page != "" {
		if album := stripQueryFragment(f.AlbumURL); album != "" {
			return page + "\t" + album
		}
		return page
	}
	if album := stripQueryFragment(f.AlbumURL); album != "" {
		return album
	}
	return RetryItemURL(f.Item)
}

// stripQueryFragment removes any query string and fragment from a URL so
// signed parameters never reach the retry list. An unparsable URL
// yields "".
func stripQueryFragment(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	query := u.Query()
	u.RawQuery = ""
	u.Fragment = ""
	if isYouTubeHost(u.Hostname()) {
		switch u.Path {
		case "/watch":
			if videoID := query.Get("v"); videoID != "" {
				u.RawQuery = url.Values{"v": []string{videoID}}.Encode()
			}
		case "/playlist":
			if playlistID := query.Get("list"); playlistID != "" {
				u.RawQuery = url.Values{"list": []string{playlistID}}.Encode()
			}
		}
	}
	return u.String()
}

func isYouTubeHost(host string) bool {
	host = strings.ToLower(host)
	return host == "youtube.com" || strings.HasSuffix(host, ".youtube.com") ||
		host == "youtube-nocookie.com" || strings.HasSuffix(host, ".youtube-nocookie.com") ||
		host == "youtu.be"
}

func isYouTubeWatchURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && isYouTubeHost(u.Hostname()) && u.Path == "/watch" && u.Query().Get("v") != ""
}

// FailureReason classifies an error into a short reason for the grouped
// summary.
func FailureReason(err error) string {
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

// FormatFailureSummary groups failures by reason, most frequent first.
func FormatFailureSummary(failures []model.FailedItem) string {
	counts := map[string]int{}
	for _, f := range failures {
		counts[FailureReason(f.Err)]++
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

// RetryReport describes a written retry list.
type RetryReport struct {
	Path    string
	Summary string
	Failed  int
	Lines   int
}

// WriteRetryReport writes a retry list of the failed items into dir. With
// no usable failures it writes nothing and returns a zero report.
func WriteRetryReport(dir string, failures []model.FailedItem, now time.Time) (RetryReport, error) {
	var usable []model.FailedItem
	var lines []string
	seen := map[string]bool{}
	for _, f := range failures {
		entry := RetryEntry(f)
		if entry == "" {
			continue
		}
		usable = append(usable, f)
		if !seen[entry] {
			seen[entry] = true
			lines = append(lines, entry)
		}
	}
	if len(lines) == 0 {
		return RetryReport{}, nil
	}

	path := filepath.Join(dir, "scraper-failed-"+now.Format("20060102-150405")+".txt")
	var list strings.Builder
	fmt.Fprintf(&list, "# scraper retry list - %s - %d failed item(s), %d retry line(s)\n",
		now.Format("2006-01-02 15:04:05"), len(usable), len(lines))
	list.WriteString("# Each line is: item URL [TAB album URL], or an album URL alone\n")
	list.WriteString("# when the item has no page of its own. Re-running downloads missing items.\n")
	for _, line := range lines {
		list.WriteString(line + "\n")
	}

	file, err := fileutil.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return RetryReport{}, fmt.Errorf("write retry list: %w", err)
	}
	if _, err := io.WriteString(file, list.String()); err != nil {
		_ = file.Close()
		return RetryReport{}, fmt.Errorf("write retry list: %w", err)
	}
	if err := file.Close(); err != nil {
		return RetryReport{}, fmt.Errorf("write retry list: %w", err)
	}

	return RetryReport{
		Path: path,
		Summary: fmt.Sprintf("Failed %d item(s):\n%s\nRetry list: %s",
			len(usable), FormatFailureSummary(usable), path),
		Failed: len(usable),
		Lines:  len(lines),
	}, nil
}
