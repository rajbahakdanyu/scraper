# Task 3 Report

## Status

Complete.

## Changes

- Filester single-file items now expose the stable `/d/<slug>` page URL. The signed URL is retained behind `MediaItem.Resolve`.
- Filester and Bunker HTML parsing now limits response reads to 32 MiB.
- `netutil.GetJSON` now limits response reads to 10 MiB.
- Unknown-length downloads are capped at 64 GiB. Exceeding the cap returns a non-retryable error, allowing the existing cleanup path to discard partial files and resume metadata.
- Added regression tests for signed URL redaction and oversized Filester, Bunker, and JSON responses.

## Commit

`26c94ab fix: stop leaking signed URL in filester single-file paths; bound untrusted reads`

## Verification

All commands used `/opt/homebrew/bin/go`:

- `gofmt -w .` passed.
- `go vet ./...` passed.
- `go test ./internal/parser/filester/ ./internal/parser/bunker/ ./internal/netutil/ ./internal/downloader/ ./cmd/` passed.
- `go test -race ./internal/parser/filester/ ./internal/parser/bunker/ ./internal/netutil/ ./internal/downloader/ ./cmd/` passed.

The repository worktree is clean after the commit.
