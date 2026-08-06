# Spec: Album Filtering

**Date:** 2026-08-06
**Status:** approved
**Scope:** Chunk D part 1 — add `--include`/`--exclude` globs and `--min-size`/`--max-size` to filter media items before download.

## Requirements

**P1.** `--include <glob>` repeatable persistent flag, also settable via config YAML (`include: [*.mp4, *.mkv]`). Glob syntax: Go's `filepath.Match`. Applied to the last path segment of each item's URL. Empty (default) = keep all. CLI overrides config (same `flags.Changed` pattern).

**P2.** `--exclude <glob>` repeatable persistent flag, also settable via config YAML. Same glob syntax and matching target as `--include`. Applied after `--include` narrows the list. CLI overrides config.

**P3.** `--min-size <size>` persistent flag and config (`min_size: 1M`). Accepts human-readable byte sizes: `500K`, `2M`, `1G` (binary/1024-based, same format as `--limit-rate`). Items with `Size == 0` (unknown) are not filtered by size. CLI overrides config.

**P4.** `--max-size <size>` persistent flag and config (`max_size: 500M`). Same format as `--min-size`. CLI overrides config.

**P5.** Filtering happens after `scraper.Parse()` returns items, before items enter the TUI or download queue. A stderr summary prints counts: total items, kept, excluded by glob, excluded by size.

**P6.** Invalid glob pattern → fatal at startup (`filepath.Match` compilation error).

**P7.** If no items survive filtering, skip download and print `No items matched filters.` to stderr. Exit clean (not an error).

**P8.** All output modes respect filters: TUI, `--no-tui`, `--json`, `--dry-run`, batch mode.

## Design

### New file: `internal/model/filter.go`

```
func FilterItems(items []MediaItem, include, exclude []string, minSize, maxSize int64) ([]MediaItem, FilterSummary)

type FilterSummary struct {
    Total          int // items before filtering
    Kept           int // items after all filters
    ExcludedByGlob int // items removed by include/exclude
    ExcludedBySize int // items removed by min/max size
}
```

Filter logic:
1. Extract filename from each item's URL (last path segment after `/`, stripped of query string)
2. Apply `--include`: if set, keep only items where filename matches ANY include glob. If not set, pass all.
3. Apply `--exclude`: remove items where filename matches ANY exclude glob.
4. Apply `--min-size`: if set, remove items where `Size > 0 && Size < minSize`.
5. Apply `--max-size`: if set, remove items where `Size > 0 && Size > maxSize`.
6. Return filtered slice and summary.

Size extraction from URL: `extractFilename(rawURL string) string` — strips query string, takes last path segment.

### Changed: `internal/config/config.go`

```
type Config struct {
    // ... existing ...
    Include []string `yaml:"include"`
    Exclude []string `yaml:"exclude"`
    MinSize string   `yaml:"min_size"`
    MaxSize string   `yaml:"max_size"`
    // minSizeBytes/maxSizeBytes: parsed int64 values, set by loadGlobalConfig.
    // Not serialized to YAML. Zero means no limit (same as empty string).
    minSizeBytes int64 `yaml:"-"`
    maxSizeBytes int64 `yaml:"-"`
}

func (c *Config) HasFilters() bool {
    return len(c.Include) > 0 || len(c.Exclude) > 0 || c.MinSize != "" || c.MaxSize != ""
}
func (c *Config) MinSizeBytes() int64  { return c.minSizeBytes }
func (c *Config) MaxSizeBytes() int64  { return c.maxSizeBytes }
```

### Changed: `cmd/root.go`

New persistent flags, parsed via Cobra `StringSliceVar` for repeatable:

```
PersistentFlags:
    root.PersistentFlags().StringSlice("include", nil, "Keep only items matching glob (repeatable, e.g. --include '*.mp4' --include '*.mkv')")
    root.PersistentFlags().StringSlice("exclude", nil, "Skip items matching glob (repeatable)")
    root.PersistentFlags().String("min-size", "", "Skip items smaller than size (e.g. 500K, 2M)")
    root.PersistentFlags().String("max-size", "", "Skip items larger than size (e.g. 500M, 1G)")
```

`globalOptions` gains: `include []string`, `includeSet bool`, `exclude []string`, `excludeSet bool`, `minSize string`, `minSizeSet bool`, `maxSize string`, `maxSizeSet bool`.

`loadGlobalConfig`: CLI overrides with `flags.Changed` pattern. If `include` patterns are invalid (filepath.Match rejects), fail at startup. Same for `exclude`.

Parsing min/max-size into bytes uses a new `config.ParseSize(s string) (int64, error)` function (extracted from existing rate-limit parsing, or shared).

### Wiring

Filtering is applied in `runWithScraper` after the scraper function runs, and in `runBatch` after each album's items are scraped. The `FilterItems` call is wrapped in the scrapeFn closure:

```go
scraperFn := func() ([]model.MediaItem, error) {
    items, err := scraper.Parse(targetURL)
    if err != nil {
        return nil, err
    }
    if cfg.HasFilters() {
        items, summary := model.FilterItems(items, cfg.Include, cfg.Exclude, cfg.MinSizeBytes, cfg.MaxSizeBytes)
        fmt.Fprintf(os.Stderr, "filters: %s\n", summary)
    }
    return items, nil
}
```

### Error handling

| Scenario | Behavior |
|---|---|
| `--include 'bad[glob'` | Fatal: `invalid --include pattern "bad[glob": syntax error in pattern` |
| No items after filtering | stderr: `No items matched filters.` + summary, exit 0 |
| `--min-size 5M --max-size 1M` | All items excluded by size (min > max), summary shows 0 kept |
| Item Size == 0 (unknown) | Not filtered by min/max-size |
| No filters set (all empty) | `FilterItems` is a no-op — all items pass through |
| `--include '*.mp4'` + video named `video.mp4?token=xyz` | Matches: `extractFilename` strips query string |

### Testing

- `internal/model/filter_test.go`: FilterItems with include, exclude, combined, size, no-match, unknown-size, empty-input
- `internal/config/config_test.go`: ParseSize with valid/invalid values
- `cmd/root_test.go`: flag existence, repeatability, invalid glob, config override
