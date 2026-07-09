# Scraper CLI

A Go-based CLI tool for scraping and downloading media from YouTube, GoFile,
CyberDrop, and Bunker, with an interactive terminal UI for managing downloads.

## Requirements

- Go 1.21 or newer (developed/tested with Go 1.26)

## Install / Build

```bash
go build -o scraper ./cmd/scraper
```

Or install directly:

```bash
go install github.com/user/scraper/cmd/scraper@latest
```

## Usage

Each site has its own subcommand. The target URL can be passed positionally
or via `--url`/`-u`.

```bash
scraper youtube https://youtube.com/watch?v=xxx --output ./downloads
scraper gofile https://gofile.io/d/xxx
scraper cyberdrop https://cyberdrop.me/a/xxx
scraper bunker https://bunkr.sv/a/xxx
```

Common flags (available on every site subcommand as persistent flags):

| Flag             | Default | Description                                  |
|------------------|---------|-----------------------------------------------|
| `-u, --url`      | (none)  | Content URL (alternative to positional arg)  |
| `-o, --output`   | `.`     | Output directory                             |
| `--concurrent`   | `5`     | Maximum concurrent downloads                 |
| `--config`       | (none)  | Config file path (default: `~/.scraper.yaml`)|
| `--ui-mode`      | `minimal` | UI mode: full, minimal, quiet              |

### Interactive TUI

Launch a full-screen dashboard that lists parsed items and lets you select
and download them interactively:

```bash
scraper tui <site> <url>
```

`<site>` is one of `youtube`, `gofile`, `cyberdrop`, `bunker`.

Keybindings inside the dashboard:

| Key             | Action                                              |
|-----------------|------------------------------------------------------|
| `↑/k`, `↓/j`    | Navigate the item list                               |
| `space`         | Toggle selection of the current item                 |
| `enter`         | Download selected items (or all, if none selected)   |
| `q`, `ctrl+c`   | Quit                                                  |

## Configuration

`internal/config` supports loading a YAML config file (default
`~/.scraper.yaml`):

```yaml
output_dir: ~/Downloads
concurrent_downloads: 10
timeout: 30s
user_agent: "Scraper/1.0"
```

The config file is loaded automatically from `~/.scraper.yaml` (or a path
specified via `--config`). CLI flags override config file values.

## Project Structure

See [docs/usage.md](docs/usage.md) for a per-command reference, and
[docs/superpowers/specs/2026-07-08-cli-scraper-design.md](docs/superpowers/specs/2026-07-08-cli-scraper-design.md)
for the original design doc.

## Known Limitations

- **YouTube**: uses the public oEmbed endpoint for metadata only; it does not
  resolve real downloadable video stream URLs. Real stream extraction is
  legally/technically involved (signature deciphering, format negotiation)
  and is out of scope here.
- **GoFile**: calls the unauthenticated `/contents/{id}` API; GoFile now
  generally requires an account token for most content, so live results may
  return an API error.
- **CyberDrop / Bunker**: use best-effort CSS selectors that may not match
  every page layout; treat them as a starting point to adapt against the
  live site markup.
- No bandwidth throttling yet; per-item pause/cancel is available via the
  downloader API but not wired into the TUI keybindings.

## Development

```bash
go build ./...
go vet ./...
go test ./...
```
