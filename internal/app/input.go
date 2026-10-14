package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Entry is one input line: a URL to scrape and, optionally, the album URL
// that item belongs to. Paired entries (retry lists) let the download land
// in its original album folder while scraping only the item.
type Entry struct {
	URL      string
	AlbumURL string
}

// ReadURLFile parses a URL list file (see ParseURLList).
func ReadURLFile(path string) ([]Entry, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open input file %q: %w", path, err)
	}
	defer f.Close()
	return ParseURLList(f)
}

// ParseURLList parses one entry per non-comment line. A line may be a
// single URL, or an item URL and its album URL separated by a tab (the
// retry-list format); anything after a second tab is ignored. Lines
// starting with "#" and blank lines are skipped.
func ParseURLList(r io.Reader) ([]Entry, error) {
	var entries []Entry
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		entry := Entry{URL: strings.TrimSpace(fields[0])}
		if len(fields) > 1 {
			entry.AlbumURL = strings.TrimSpace(fields[1])
		}
		if entry.URL == "" {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read URLs: %w", err)
	}
	return entries, nil
}

// SplitGroups separates YouTube entries from the rest. Mixed lists run as
// sequential same-type groups, YouTube first, each with its own downloader.
func SplitGroups(entries []Entry) (youtube, generic []Entry, err error) {
	for _, e := range entries {
		site, err := DetectSite(e.URL)
		if err != nil {
			return nil, nil, err
		}
		if site == "youtube" {
			youtube = append(youtube, e)
		} else {
			generic = append(generic, e)
		}
	}
	return youtube, generic, nil
}
