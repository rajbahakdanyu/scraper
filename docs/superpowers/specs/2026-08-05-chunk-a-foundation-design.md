# Chunk A: Foundation — Proxy Support + Shell Completions + Version

Date: 2026-08-05 | Status: approved

## Overview

Two independent foundations:

- **Proxy support**: `--proxy` flag / config file to route all HTTP traffic through a proxy.
  Currently `netutil.client.go:127` hardcodes `transport.Proxy = nil`.
- **Shell completions + version**: Cobra generates completion scripts for
  bash/zsh/fish/powershell nearly for free. A `version` subcommand helps
  with bug reports.

## Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| P1 | `--proxy` persistent flag sets an HTTP/S proxy for all outbound requests | P0 |
| P2 | Config file field `proxy:` sets a default; CLI flag overrides | P0 |
| P3 | Invalid proxy URL fails fast before any network activity | P0 |
| P4 | Empty/missing proxy → no proxy (unchanged behavior) | P0 |
| P5 | `scraper completion [bash\|zsh\|fish\|powershell]` prints completion script | P0 |
| P6 | `scraper version` prints version string, overridable at build via `-ldflags` | P0 |

## Flag interaction

`--proxy` follows the same pattern as `--output`, `--concurrent`:
- Uses `flags.Changed("proxy")` to detect explicit CLI set
- CLI flag overrides config file value
- Zero-value (empty string) does NOT override config when flag is unset

## Architecture

```
--proxy flag / config file
  │
  ├─ globalOptions.proxy / proxySet
  ├─ config.Config.Proxy (YAML: proxy)
  ├─ loadGlobalConfig: flag overrides config
  │
  └─ netutil.NewPublicClientWithProxy(timeout, proxyURL)
       └─ newPublicClient(..., proxyURL)
            └─ transport.Proxy = http.ProxyURL(u)  [was hardcoded nil]
                 ├─ invalid URL → Parse error, fail fast before any requests
                 └─ valid URL → built into all HTTP clients

completion / version subcommands
  │
  └─ root.AddCommand(completionCmd, versionCmd)
```

Proxy: Existing `NewPublicClient` and `NewPublicStreamingClient` constructors
remain unchanged. New `WithProxy` variants are used by downloader constructors
when `cfg.Proxy != ""`.

Completions/version: use Cobra's built-in `GenBashCompletion` / `GenZshCompletion`.
No external dependencies.

## Files changed

| File | Change | Lines |
|------|--------|-------|
| `cmd/root.go` | `--proxy` flag + globalOptions fields; `version` + `completion` subcommands | ~50 |
| `internal/config/config.go` | `Proxy string` YAML field | ~5 |
| `internal/netutil/client.go` | `NewPublicClientWithProxy`, `NewPublicStreamingClientWithProxy`; newPublicClient accepts proxyURL | ~25 |
| `internal/downloader/downloader.go` | Pass cfg.Proxy to netutil client constructor | ~5 |
| `internal/parser/youtube/youtube.go` | Pass cfg.Proxy to netutil client constructor | ~5 |
| `cmd/root_test.go` | Flag registration tests for proxy, version, completion | ~15 |
| `internal/config/config_test.go` | Proxy YAML round-trip | ~10 |
| `internal/netutil/client_test.go` | Proxy set on transport; invalid URL error | ~15 |

~130 lines total.

## Error handling

- **Invalid proxy URL** (unparseable): error at startup, before any network requests
- **Empty proxy** (flag not set + config empty): no proxy, existing `transport.Proxy = nil` behavior preserved
- **Proxy connection failures**: standard Go HTTP client error (context deadline, connection refused) — same as any network failure

## Version

```go
var version = "dev"  // overridden via -ldflags "-X .../cmd.version=v0.2.0"
```

`scraper version` prints `scraper v0.2.0` (or `scraper dev` from dev build).

## Testing

| Test | What it verifies |
|------|-----------------|
| `TestRootCmdHasProxyFlag` | `--proxy` persistent flag registered |
| `TestLoadGlobalConfigProxyOverride` | CLI `--proxy` overrides config file |
| `TestNewPublicClientWithProxy` | Transport.Proxy matches `http.ProxyURL` |
| `TestNewPublicClientWithProxyInvalidURL` | Error for unparseable URL |
| `TestNewRootCmdVersionCompletion` | `version` and `completion` subcommands exist |
| `TestConfigProxyRoundTrip` | YAML proxy field reads back correctly |
