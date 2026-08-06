# Task 20 Report

Implemented the `newSiteCmd` factory and root orchestration deduplication.

## Changes

- Added `newSiteCmd` in `cmd/root.go` using the existing zero-argument scraper constructor shape.
- Registered all five site commands directly in `NewRootCmd`.
- Reduced the five site command files to test-compatible registration shims.
- Extracted shared downloader construction for YouTube and generic downloaders.
- Extracted shared JSON, plain, and TUI dispatch with notification handling.
- Replaced the scraper registry proxy/cookies/plain branching with a constructor table loop.

## Verification

- `/opt/homebrew/bin/gofmt -w $(git ls-files '*.go')` passed.
- `/opt/homebrew/bin/go vet ./...` passed.
- `/opt/homebrew/bin/go test ./cmd/` passed.
- `/opt/homebrew/bin/go test -race ./cmd/` passed.
- `git diff --check` passed.
