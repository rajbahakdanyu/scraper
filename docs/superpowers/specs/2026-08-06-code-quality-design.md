# Spec: Code-Quality Improvements

**Date:** 2026-08-06
**Status:** approved
**Scope:** All 7 code-quality improvements from BACKLOG.MD.

## Requirements

### C1. Deduplicate HTTP boilerplate

**C1a.** Move `nonRetryableError` type and `isRetryableStatus` from each parser and downloader into `internal/model/retry.go`. All packages import from `model`.

**C1b.** Move `sanitizeFilename` from `internal/downloader/downloader.go` and `internal/parser/youtube/youtube.go` into `internal/fileutil/filename.go`. Both import from `fileutil`.

**C1c.** Move `backoffWithJitter(base, attempt)` from `internal/downloader/downloader.go`, `internal/parser/filester/filester.go`, and `internal/parser/youtube/youtube.go` into `internal/netutil/backoff.go`. All import from `netutil`.

**C1d.** Add `GetJSON(ctx context.Context, client *http.Client, url string, target interface{}) error` to `internal/netutil/client.go`. Handles: `http.NewRequestWithContext` → `client.Do` → status check (non-2xx returns error) → `json.NewDecoder` → `target`. Replaces ad-hoc request loops in cyberdrop, bunker, goonbox, and filester parsers.

### C2. Unify retry semantics

Covered by C1a — moving `nonRetryableError` to `model` makes it the single retry classification. No further work needed beyond C1a.

### C3. Speed up tests

**C3a.** Add `Sleeper` interface to `internal/downloader/downloader.go` (or `internal/netutil/`):

```
type Sleeper interface { Sleep(d time.Duration) }
type realSleeper struct{}
func (realSleeper) Sleep(d time.Duration) { time.Sleep(d) }
```

The downloader stores a `Sleeper` (default: `realSleeper{}`). Tests inject a no-op sleeper. Apply to all `time.Sleep` calls in backoff loops.

**C3b.** Apply same pattern to `internal/parser/filester/filester.go` backoff loop.

**C3c.** Expected result: downloader tests from ~21s → <1s, filester tests from ~18s → <1s.

### C4. Worker pool

Not implemented in this spec. The tea.Cmd-per-item pattern is inherent to Bubble Tea's architecture and replacing it with a worker pool requires significant restructuring of the dashboard's model/update/view loop. Deferred to a separate design iteration.

### C5. iota fix

Remove redundant `= iota` from `internal/model/types.go` lines 23-26. Only the first const in the block needs `= iota`:

```go
const (
    StatusPending     DownloadStatus = iota // iota = 0
    StatusDownloading                      // iota = 1
    StatusCompleted                        // iota = 2
    StatusFailed                           // iota = 3
    StatusCancelled                        // iota = 4
)
```

### C6. Rate-limiter fairness

Not implemented in this spec. Rate-limiter fairness requires per-connection token reservation or weighted fair queuing — significant complexity for a minor improvement. The current shared bucket works correctly for the common case of similarly-sized downloads.

### C7. File permissions

**C7a.** Change all `0600` file writes to `0644` (readable by owner + group/others):

| File | Lines |
|---|---|
| `internal/downloader/downloader.go` | `os.OpenFile(..., 0600)` → `0644` |
| `internal/downloader/downloader.go` | `os.WriteFile(..., 0600)` → `0644` |
| `internal/parser/youtube/youtube.go` | `os.OpenFile(..., 0600)` → `0644` |
| `internal/fileutil/checksum.go` | `temp.Chmod(0600)` → `0644` |

**C7b.** Change all `0750` directory creates to `0755`:

| File | Lines |
|---|---|
| `internal/ui/dashboard/dashboard.go` | `os.MkdirAll(..., 0750)` → `0755` |
| `cmd/plain.go` | `os.MkdirAll(..., 0750)` → `0755` |

**C7c.** (No config file mode option in this spec — YAGNI. Media files are inherently shareable; `0644`/`0755` is the right default.)

### Testing

- C1: existing parser/downloader tests must pass unchanged
- C3: downloader + filester tests run with injected sleeper, verify <1s
- C5: existing tests pass (enum values unchanged)
- C7: file mode assertions in tests where applicable

### Error handling

C1a-C1d are pure refactors: no behavior change. Existing error handling preserved. `GetJSON` wraps `client.Do` errors and non-2xx statuses as `fmt.Errorf` errors matching the current parser patterns.
