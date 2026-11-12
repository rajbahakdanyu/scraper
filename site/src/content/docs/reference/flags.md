---
title: Flags
description: Every command-line flag and subcommand.
---

```
scraper [flags] [url...]
scraper <site> [flags] <url>
```

Flags override [config file](/scraper/reference/configuration/) values.

## Output

| Flag | Default | Description |
| --- | --- | --- |
| `-o, --output` | `~/Downloads` | Output directory |
| `-u, --url` | | Content URL instead of a positional argument (site subcommands only) |
| `-i, --input-file` | | Read URLs from a file, one per line; an optional tab-separated album URL keeps retries in the album folder |
| `--config` | `~/.scraper.yaml` | Config file path |
| `-n, --dry-run` | `false` | List item count and total size without downloading |
| `--print-urls` | `false` | Print item URLs, one per line, and exit |
| `--no-tui` | `false` | Plain-text output instead of the dashboard |
| `-j, --json` | `false` | JSON Lines output; implies `--no-tui` |
| `--log-file` | | Append a timestamped run log |
| `--notify` | `false` | Desktop notification when downloads finish |
| `--watch` | | Re-run the input list every interval (e.g. `30m`) until interrupted; implies `--no-tui` |

## Network

| Flag | Default | Description |
| --- | --- | --- |
| `--concurrent` | `3` | Maximum concurrent downloads |
| `--per-host` | `2` | Maximum concurrent downloads from one server; `0` = no limit |
| `--limit-rate` | unlimited | Cap combined download speed, e.g. `500K`, `2M`, `1G` |
| `--scrape-delay` | | Pause between album scrapes, e.g. `500ms` |
| `--proxy` | | HTTP/S proxy for all requests, e.g. `http://proxy:8080` |
| `--cookies` | | Netscape-format cookies file |
| `--cookies-from-browser` | | Import cookies for supported sites from a local browser profile (`firefox`; macOS, Linux including snap/flatpak, Windows) |

## Filtering

| Flag | Default | Description |
| --- | --- | --- |
| `--media-only` | `true` | Only media and archive files; `--media-only=false` allows everything |
| `--allow-ext` | | Extra extensions to keep with media-only (repeatable) |
| `--include` | | Glob patterns to keep (repeatable) |
| `--exclude` | | Glob patterns to skip (repeatable) |
| `--min-size` | | Minimum file size, e.g. `1K`, `10M` |
| `--max-size` | | Maximum file size |
| `--max-album-items` | `10000` | Cap items from one album; `0` = unlimited |
| `--disk-check` | `true` | Check free space before downloading |
| `--checksums` | `false` | Write and verify `.sha256` sidecar files |

See [Filtering](/scraper/guides/filtering/) for details.

## YouTube

| Flag | Default | Description |
| --- | --- | --- |
| `--quality` | best | Maximum resolution: `2160p`, `1440p`, `1080p`, `720p`, `480p`, `360p`, `240p` |
| `--audio-only` | off | Audio only; `m4a` (default) or `mp3` |
| `--youtube-backend` | `auto` | `auto` (built-in, falls back to yt-dlp), `builtin`, or `yt-dlp` |

## Subcommands

| Subcommand | Description |
| --- | --- |
| `youtube`, `cyberdrop`, `bunker`, `goonbox`, `filester`, `pixeldrain`, `turbo` | Force the site for a URL |
| `version` | Print the version |
| `completion [bash\|zsh\|fish\|powershell]` | Generate a shell completion script |
