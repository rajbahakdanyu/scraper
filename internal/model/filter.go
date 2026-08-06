package model

import (
	"fmt"
	"path/filepath"
	"strings"
)

type FilterSummary struct {
	Total          int
	Kept           int
	ExcludedByGlob int
	ExcludedBySize int
}

func (s FilterSummary) String() string {
	var exclusionParts []string
	if s.ExcludedByGlob > 0 {
		exclusionParts = append(exclusionParts, fmt.Sprintf("%d excluded by glob", s.ExcludedByGlob))
	}
	if s.ExcludedBySize > 0 {
		exclusionParts = append(exclusionParts, fmt.Sprintf("%d excluded by size", s.ExcludedBySize))
	}
	if len(exclusionParts) > 0 {
		return fmt.Sprintf("%d/%d items (%s)", s.Kept, s.Total, strings.Join(exclusionParts, ", "))
	}
	return fmt.Sprintf("%d/%d items", s.Kept, s.Total)
}

func FilterItems(items []MediaItem, include, exclude []string, minSize, maxSize int64) ([]MediaItem, FilterSummary) {
	s := FilterSummary{Total: len(items)}
	if s.Total == 0 {
		return nil, s
	}

	hasInclude := len(include) > 0
	hasExclude := len(exclude) > 0
	hasSize := minSize > 0 || maxSize > 0

	if !hasInclude && !hasExclude && !hasSize {
		s.Kept = s.Total
		return items, s
	}

	filtered := make([]MediaItem, 0, len(items))
	for _, item := range items {
		filename := extractFilename(item.URL)

		if hasInclude && !matchesAny(filename, include) {
			s.ExcludedByGlob++
			continue
		}
		if hasExclude && matchesAny(filename, exclude) {
			s.ExcludedByGlob++
			continue
		}
		if item.Size > 0 {
			if minSize > 0 && item.Size < minSize {
				s.ExcludedBySize++
				continue
			}
			if maxSize > 0 && item.Size > maxSize {
				s.ExcludedBySize++
				continue
			}
		}
		filtered = append(filtered, item)
	}
	s.Kept = len(filtered)
	return filtered, s
}

func extractFilename(rawURL string) string {
	if idx := strings.Index(rawURL, "?"); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	base := rawURL
	if idx := strings.LastIndex(rawURL, "/"); idx >= 0 {
		base = rawURL[idx+1:]
	}
	return base
}

func matchesAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if matched, _ := filepath.Match(p, name); matched {
			return true
		}
	}
	return false
}
