# Task 16 Report

Implemented the timing-sensitive test deflake changes.

## Changes

- `TestCancel` now uses an injected, context-aware sleeper and cancels while retry backoff is blocked.
- `TestSetRateLimitThrottlesThroughput` checks deterministic progress transfers instead of elapsed wall time.
- `TestShutdownWithConcurrentDownloads` waits for all four active response bodies before shutdown.
- Plain-mode signal handling is exposed through `watchPlainShutdown`; the test invokes it directly without sending a process signal.
- YouTube shutdown synchronization uses a fake-server stream-start channel instead of polling.

## Verification

Passed:

```text
/opt/homebrew/bin/go test -race -count=5 ./internal/downloader/ ./cmd/ ./internal/parser/youtube/
```

All three packages passed across five runs.
