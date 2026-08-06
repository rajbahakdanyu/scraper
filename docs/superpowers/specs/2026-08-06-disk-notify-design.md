# Spec: Disk-Space Preflight + Completion Notification

**Date:** 2026-08-06
**Status:** approved
**Scope:** Chunk D part 2 — disk-space preflight warning and OS completion notification.

## Requirements

### Disk-space preflight

**P1.** `--disk-check` persistent bool flag, default `true`. Also settable via config YAML (`disk_check: false`). CLI overrides config (same `flags.Changed` pattern).

**P2.** After `scraper.Parse()` returns items (and after any `FilterItems` filtering), compute total estimated download size from `MediaItem.Size`. Check free disk space on the output directory's filesystem mount point.

**P3.** If estimated size > 0 and free space < estimated size, print warning to stderr:
```
disk: 2.5G free, 3.1G needed (600M short)
```
If some items have unknown size (`Size == 0`), append:
```
disk: 2.5G free, 3.1G needed (600M short) (2 items with unknown size)
```
Downloads proceed regardless — this is a warning, not a blocker.

**P4.** If total estimated size is 0 (all items unknown size), skip the check (no warning).

**P5.** Free space check uses `golang.org/x/sys/unix.Statfs` for macOS/Linux. Only runs on Unix platforms; non-Unix silently skips.

**P6.** Integration point: in `runWithScraper` and `runBatch` scrape/parse flow, after items are available and filtering is applied, before download starts. Also applies to `--dry-run` mode.

### Completion notification

**P7.** `--notify` persistent bool flag, default `false`. Also settable via config YAML (`notify: true`). CLI overrides config.

**P8.** When all downloads in a run finish (single-URL or batch), if `--notify` is set, fire an OS notification:
- **macOS:** `osascript -e 'display notification "<summary>" with title "scraper"'`
- **Linux:** `notify-send "scraper" "<summary>"`

**P9.** Summary format:
- All ok: `Done: 5 items (150M)`
- With failures: `Done: 5 items, 2 failed`

**P10.** If the platform notification tool is missing (`osascript` or `notify-send`), silently skip the notification. Never fail the run due to missing notification tool.

**P11.** Notification fires in all output modes: TUI, `--no-tui`, `--json`, batch mode.

## Design

### New file: `internal/notify/notify.go`

```
func Send(summary string) error
  — platform detection: macOS uses osascript, Linux uses notify-send
  — if tool missing, returns nil (no error)
  — if tool runs but exits non-zero, returns error (logged but not fatal)
```

### Changed: `internal/config/config.go`

```
type Config struct {
    // ... existing ...
    DiskCheck bool `yaml:"disk_check"`
    Notify    bool `yaml:"notify"`
}
```

### New file: `internal/disk/check.go`

```
func FreeSpace(path string) (int64, error)
  — uses unix.Statfs to get available blocks * block size
  — on non-Unix, returns (0, nil)
```

### New file: `internal/disk/format.go`

```
func FormatBytes(n int64) string
  — human-readable: "2.5G", "600M", "500K"
  — matches HumanizeBytes from dashboard
```

### Changed: `cmd/root.go`

New persistent flags:
```
root.PersistentFlags().Bool("disk-check", true, "Warn if free disk space is less than estimated download size")
root.PersistentFlags().Bool("notify", false, "Send OS notification when downloads complete")
```

`globalOptions`: `diskCheck/bool + diskCheckSet`, `notify/bool + notifySet`.

`loadGlobalConfig`: CLI overrides. No validation needed (simple bools).

### Integration

**Disk preflight** runs in `scrapeFn` closure within `runWithScraper`, after `FilterItems`:

```go
if cfg.DiskCheck && !opts.dryRun {
    total := sumItemSizes(items)
    if total > 0 {
        free, err := disk.FreeSpace(cfg.OutputDir)
        if err == nil && free < total {
            short := total - free
            msg := fmt.Sprintf("disk: %s free, %s needed (%s short)",
                disk.FormatBytes(free), disk.FormatBytes(total), disk.FormatBytes(short))
            if unknown := countUnknownSizes(items); unknown > 0 {
                msg += fmt.Sprintf(" (%d items with unknown size)", unknown)
            }
            fmt.Fprintln(os.Stderr, msg)
        }
    }
}
```

**Completion notification** fires at the end of each mode:
- For `runWithScraper` TUI: after `p.Run()` returns (all downloads done)
- For `runPlain`/`runJSON`: at end of function, after all downloads
- For batch: at end of `runBatch`/`runBatchPlain`/`runBatchJSON`/batch dashboard, after all albums complete

Summary counts from the downloader's final state: total items, total size, number failed.

### Error handling

| Scenario | Behavior |
|---|---|
| `go build` on non-Unix (Windows) | `unix.Statfs` import fails — use build tags (`//go:build unix`) |
| Free space check fails (permissions) | Silently skip the warning |
| `--notify` on macOS without osascript | Silently skip (unlikely — osascript is always present) |
| `--notify` on Linux without notify-send | Silently skip |
| Notification tool exits non-zero | Log error to stderr, don't fail the run |
| Unknown total size (all items Size==0) | Skip disk check entirely |

### Testing

- `internal/disk/check_test.go`: FreeSpace on temp dir, FormatBytes edge cases
- `internal/notify/notify_test.go`: Send with mock command runner, platform detection
- `cmd/root_test.go`: flag existence, config override for disk-check and notify
