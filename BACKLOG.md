# Backlog

Feature suggestions and code-quality improvements from the 2026-08-01 code
review. Bugs from that review were fixed in commits following this file's
addition; this file tracks the remaining non-bug work.

## Feature suggestions (ranked by value)

1. **Batch mode**: `--input-file urls.txt` (or multiple positional URLs /
   stdin) — process many albums in one run with a shared download queue.
2. **Script-friendly output**: `--no-tui` / `--json` flags for pipelines and
   cron (the non-TTY auto-fallback half of this was fixed; explicit flags and
   machine-readable output remain).
3. **TUI retry + failure inspection**: `r` retries failed items; `enter`
   shows the selected item's actual error message (currently discarded in
   `applyDone`, users only see a bare "failed").
4. **YouTube quality & audio controls**: `--quality 1080p`,
   `--audio-only` (m4a/mp3 extraction — ffmpeg is already a dependency).
5. **Album filtering**: `--include`/`--exclude` globs or extensions,
   `--min-size`/`--max-size`. Pairs well with `--dry-run`.
6. **Shell completions + `version` command** — Cobra generates completions
   nearly for free; `version` helps bug reports.
7. **Disk-space preflight** — total size is known for most sites after
   parsing; check free space before starting a multi-GB album.
8. **Auth support**: `--cookies <file>` / `--cookies-from-browser` for
   private albums (currently impossible on the cyberlocker sites).
9. **Completion notification** (ntfy.sh / OS notification) for long batches.
10. **Proxy support flag** — `netutil` deliberately sets `transport.Proxy =
    nil`; users behind corporate proxies need an explicit `--proxy`.

## Code-quality improvements

1. **Deduplicate parser HTTP boilerplate** — every parser hand-rolls:
   build request → set UA → `client.Do` → status check → `json.NewDecoder`.
   A shared `netutil.GetJSON` helper plus shared `backoffWithJitter`
   (duplicated in `internal/downloader` and `internal/parser/filester`) and
   `sanitizeFilename` (duplicated in `internal/downloader` and
   `internal/parser/youtube`) removes ~150 lines.
2. **Unify retry semantics** — the downloader's `nonRetryableError`
   distinction should be shared; parsers each classify errors ad hoc.
3. **Speed up tests** — downloader (~21s) and filester (~18s) tests sleep
   real backoff delays. Inject a clock/sleeper to make them milliseconds.
4. **Worker pool instead of one goroutine per item** —
   `startAllDownloads` batches one tea.Cmd per item; a 10k-item album spawns
   10k goroutines blocked on the semaphore. A bounded worker pool scales
   better.
5. `internal/model/types.go` — redundant `= iota` on every line of the
   `DownloadStatus` const block.
6. **Rate-limiter fairness** — the shared token bucket is first-come
   first-served; one fast connection can starve the others.
7. **File permissions** — downloads are `0600` / dirs `0750`; consider
   `0644`/`0755` (or a config option) for media meant to be shared.
