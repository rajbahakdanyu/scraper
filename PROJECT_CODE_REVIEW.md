# Code Review: `scraper` (Go CLI media downloader)

_Reviewed at commit `9c23927` (branch `main`). ~18.6k LOC across 70 Go files._

**Baseline:** `go build ./...`, `go vet ./...`, and `go test ./...` all pass cleanly (19 packages).
Overall a well-structured, defensively-coded codebase with unusually good security posture
(dial-time SSRF checks, bounded body reads, no-follow `.part` files, atomic sidecar writes,
no secrets in logs). Findings below are ordered by severity.

---

## Critical / High

### 1. Local/private HTTP proxies are completely broken — verified
`internal/netutil/client.go:233-247`

The proxied client's `DialContext` is the same SSRF-guarded dialer that requires every dialed
host to resolve to a **public** IP. When a proxy is configured, `net/http` dials the *proxy*
host through `DialContext` — and the proxy constructor passes no `allowedPrivateURLs`, so a
proxy on `127.0.0.1`, a private LAN, or Docker network is rejected.

Confirmed empirically:
```
GET through local proxy error = Get "http://example.com/": proxyconnect tcp:
host "127.0.0.1" did not resolve to a public address
```

This directly breaks the README's own recommended workflow ("use `--proxy` to switch IPs" for
Pixeldrain throttling, e.g. an SSH tunnel / local proxy). Tests only cover a *public* proxy
hostname and a private *target*, so the gap is invisible.
**Fix:** allow the proxy host itself (add it to the policy's private allowlist in
`newPublicClientWithConcurrency`, or skip the IP check on the proxy dial path).

### 2. Unbounded decode of Bunker's signing response
`internal/parser/bunker/bunker.go:276-291`

Every other JSON path funnels through `GetJSON`/`PostJSON` with `io.LimitReader(..., 10<<20)`,
but the signing response is decoded raw:
```go
if err := json.NewDecoder(signResp.Body).Decode(&sign); err != nil {
```
A hostile/broken signing server can stream an unbounded `"token"` string and OOM the process
(per-read deadline resets each read, so a slow drip also pins the goroutine).
**Fix:** wrap in `io.LimitReader`.

### 3. A garbage `.sha256` sidecar permanently blocks re-download
`internal/fileutil/checksum.go:66-75` + `internal/downloader/downloader.go:155-158`

`VerifyChecksum` returns a hard error for an empty/whitespace sidecar or a tab-separated
digest, and the downloader turns that into an item failure **without redownloading**. A
truncated sidecar (disk-full remnant, `sha256sum` with tab separator) bricks that file until
manually deleted, while a sidecar naming a *different* file is correctly treated as absent.
Inconsistent trust model.
**Fix:** return `present=false` for unparseable content (same as wrong-name) so it
re-verifies/redownloads.

### 4. "Download complete" notification fires even on failure
`cmd/root.go:689-702`

```go
err = tuiRun()/plainRun()/jsonRun()
if cfg.Notify { tryNotify(cfg, "Download complete") }
```
`err` is ignored; users get a success desktop notification after a total failure. Also,
`tryNotify` re-checks `cfg.Notify` redundantly.
**Fix:** only notify when `err == nil` (or send a failure message otherwise).

---

## Medium

### 5. Goonbox pagination can silently drop already-fetched pages
`internal/parser/goonbox/goonbox.go:197-215` — the aggregation loop `break`s on the first page
reporting `current >= last`, discarding any later results already fetched before a
hostile/inconsistent page shrank `lastPage` (lines 180-182). Data loss with no error.
Better: filter placeholder pages rather than `break`. (Contrived trigger, but untested.)

### 6. Filester doesn't retry real HTTP errors on pages > 1
`internal/parser/filester/filester.go:286-288` vs `310`

The retry machinery only catches "HTTP 200 but zero items". A 429/5xx/network blip on any page
after the first is returned immediately and the page is skipped (`parseFolder:212-221`). This
contradicts the comment at `:273-282` that claims retries protect against rate limiting — the
canonical 429 bypasses them. Pages are lost (surfaced only as `PartialError`).
**Fix:** route non-permanent `fetchDoc` errors through `Retry` for pages > 1.

### 7. `Policy` is not actually an allowlist
`internal/netutil/client.go:127-130` — `NewPolicy(allowedURLs...)` only *permits private IPs*
for listed hosts; **any public host still passes**. Callers like `filester.go:428` and
`bunker.go:198,258` read as if they pin the CDN/API to known infrastructure, but a hostile page
can point downloads at any public host. Dial-time private-IP blocking limits blast radius, but
the naming/intent mismatch is a real containment gap. Add a strict host-match mode where that's
the intent.

### 8. `SanitizeFilename` has no length clamp or Windows reserved-name handling
`internal/fileutil/filename.go:7-19` — scraped titles >255 bytes fail with `ENAMETOOLONG`;
`CON`/`NUL`/trailing-dot/space names misbehave on Windows (which is a supported build target
per the `*_other` files). Add a byte-budget truncation with a short hash suffix and
reserved-name guarding.

### 9. `open_nofollow_other.go` second check is dead code
`internal/fileutil/open_nofollow_other.go:23-27` — after `os.OpenFile` succeeds, `f.Stat()`
resolves the handle, so `Mode()&os.ModeSymlink` can never be set. It adds false confidence while
the Lstat→OpenFile TOCTOU window remains. (Unix path is correctly `O_NOFOLLOW`.)

### 10. `isPublicIP` misses IANA special-purpose ranges
`internal/netutil/client.go:288-291` — CGNAT `100.64/10`, TEST-NETs, `240.0.0.0/4`, and
broadcast pass as public. Link-local metadata (`169.254`) is correctly blocked, so impact is
low, but corporate CGNAT is reachable.

### 11. Multiple "assert nothing" tests
- `internal/notify/notify_test.go:9-38` fires real desktop notifications and only `t.Log`s —
  can never fail. The injection-safe AppleScript composition is untested.
- `internal/disk/check_test.go:19-24` logs instead of asserting for a nonexistent path.
- `internal/ui/dashboard/dashboard_test.go:563-584` (`TestBatchHeaderCursorSkip`) is vacuous,
  while `moveUpSkippingHeaders` (`dashboard.go:585-593`) actually can park the cursor on row 0's
  header.

### 12. Non-batch dashboard ticker re-arms forever
`internal/ui/dashboard/dashboard.go:266-277` — the 500 ms ticker keeps redrawing after all
downloads complete (the sibling `refreshTickMsg` branch is gated on `spinnerActive()`; this one
isn't). Batch mode has no such ticker, so behavior differs by mode.

---

## Low / Nits (real-bug adjacent)

- `internal/downloader/downloader.go:222-224`: if `os.Rename` fails, no `StatusFailed` progress
  is emitted and the temp files aren't cleaned — the item is left "Downloading" in the UI.
- `internal/parser/turbo/turbo.go:179-183`: resolved sign URL is returned without `ValidateURL`
  (inconsistent with every other parser).
- `internal/parser/bunker/bunker.go:181-184`: `path.Base` on the raw href lets `?query` leak into
  the API `fileID`.
- `internal/netutil/cookies.go:89`: `WithCookies` sets cookies only for `https://` — never sent
  to http targets.
- `internal/model/types.go:128-129`: `PartialError.Error()` panics on a zero-value
  `PartialError{}` (only `NewPartialError` is safe).
- `internal/disk/check_unix.go:14-18`: negative `Bavail` (signed on darwin) wraps to ~9.2 EB via
  unsigned multiply.
- `internal/model/filter.go:49,77`: filter globs match the raw (non-URL-decoded) slug,
  case-sensitively; `matchesAny` swallows `filepath.Match` errors — **though**
  `cmd/root.go:146-155` validates patterns up-front for all CLI/config input, so the user-facing
  impact is limited to library callers.
- `cmd/root.go:390`: `--output` help default is `.` while the effective default is `~/Downloads`
  (cobra displays the placeholder).
- `cmd/root.go:375-383`: single-URL path calls `loadGlobalConfig` twice (double parse/cookie
  read).
- `internal/ui/dashboard/dashboard.go:822-832`: `ensureStateIndex` rebuilds the full map on every
  progress event in batch mode (O(n) per event).
- `internal/fileutil/open_nofollow_unix.go:17`: raw `unix.Open` omits `O_CLOEXEC`.

---

## What's done well

- SSRF defense is genuinely strong: resolve-and-filter public IPs at dial time, scheme
  validation, https→http redirect downgrade block, credentials-in-URL rejection.
- Body-size caps (10 MiB JSON / 32 MiB HTML), stream-truncation detection, `MaxUnknownFileSize`.
- Symlink defenses on `.part` and album dirs; atomic CreateTemp+Sync+Rename for checksum/resume
  sidecars.
- Lazy signed-URL resolution with 403 re-resolve is a thoughtful design.
- Test suite is broad and mostly behavioral (YouTube fake server, ffmpeg stubs, pagination
  hostile data, SSRF redirect tests).

## Suggested priority

1. Fix proxied clients (blocks a documented feature) — **#1**
2. Notify-on-error and checksum-sidecar bricking — **#3, #4**
3. Bound the Bunker sign body (one-line) — **#2**
4. Silent page/album loss under hostile or rate-limited upstreams — **#5, #6**
5. Strict allowlist + filename limits/special tests — **#7, #8, #11**

_Review was read-only; no repo files were modified (the one temporary verification test was
removed)._
