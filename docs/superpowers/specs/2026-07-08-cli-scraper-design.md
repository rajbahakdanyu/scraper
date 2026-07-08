# CLI Media Scraper Design

## Overview
A Go-based CLI tool for scraping and downloading images/videos from YouTube, GoFile, CyberDrop, and Bunker.

## Architecture

### Project Structure
```
scraper/
├── cmd/
│   ├── root.go       # Main cobra command
│   ├── youtube.go    # YouTube subcommand
│   ├── gofile.go     # GoFile subcommand
│   ├── cyberdrop.go  # CyberDrop subcommand
│   └── bunker.go     # Bunker subcommand
├── internal/
│   ├── downloader/   # Concurrent download manager
│   ├── parser/       # Site-specific parsing logic
│   ├── ui/           # Rich terminal UI components
│   └── config/       # Configuration management
└── go.mod
```

### Components

**1. Root Command**
- Uses Cobra for CLI framework
- Global flags: `--output`, `--concurrency`, `--config`, `--ui-mode` (full, minimal, quiet)
- Error handling and logging setup
- Optional full TUI mode (toggled with `--ui-mode full` or auto-detected from environment)

**2. Site Scrapers (internal/parser/)**
- Each site has dedicated parser
- Returns standardized media metadata (title, URL, size, type, thumbnail)
- Handles site-specific rate limits/anti-scraping with circuit breaker pattern
- Includes metadata enrichment (YouTube: view count, GoFile: file description, etc.)

**3. Downloader (internal/downloader/)**
- Goroutine-based concurrent downloads with worker pool
- Progress tracking per file (bytes, speed, ETA, checksum verification)
- Retry logic with exponential backoff and jitter
- File naming and organization (sanitized filenames, subdirectories by site/type)
- Resume support for partial downloads (where supported)
- Bandwidth throttling options

**4. Terminal UI (internal/ui/)**
- Full-screen TUI application using `charmbracelet/bubbletea` (primary) with fallback to `termui`
- Multiple views: Dashboard (overview), Queue (detailed list), Logs, Settings
- Interactive download queue: pause/resume/cancel individual items, reorder priorities
- Real-time statistics panel: total speed, total downloaded, time elapsed, estimated completion
- Keybindings: space (toggle play/pause), 'c' (cancel selected), 'p' (properties), '?' (help), arrow keys/enter (navigation)
- Theme system: dark, light, high-contrast, customizable colors via config
- Rich formatting: progress bars with gradients, emojis/icons for status, file type indicators
- Tooltips and detailed views on hover/focus (showing full URLs, metadata)
- Live log view with filtering (info/warn/error) and search
- Configurable refresh rates for smooth animation
- Accessibility considerations: screen-reader friendly labels, color-blind modes

## Command Interface

```
scraper <site> <url> [flags]

Subcommands:
  youtube     Scrape YouTube videos
  gofile      Scrape GoFile files
  cyberdrop   Scrape CyberDrop galleries
  bunker      Scrape Bunker content

Flags:
  --url       Content URL to scrape
  --output    Output directory (default: current)
  --format    Media format filter (mp4, mp3, jpg, png)
  --max       Maximum items to download (default: all)
  --concurrent Max concurrent downloads (default: 5)
  --quiet     Suppress UI, machine-readable output
  --config    Config file path
```

## Data Flow

1. Parse arguments and validate URL (or launch TUI if no URL provided)
2. TUI Dashboard mode: show site selection, recent downloads, settings
3. Fetch page HTML/content
4. Extract media URLs and metadata (async, with loading spinner)
5. Display preview with counts/format info in a scrollable table
6. User confirms/selects items (multi-select with checkboxes in TUI)
7. Spawn download goroutines with semaphore/worker pool
8. Update progress bars and stats in real-time within TUI
9. Save files with original or sanitized names (organized by site/type)
10. Generate summary report (in-app modal + optional export to markdown/JSON)

## Error Handling

- Network failures: Retry up to 3 times
- Rate limiting: Backoff and retry
- Invalid URLs: Clear error message with examples
- Partial downloads: Clean up and retry
- Keyboard interrupt: Graceful shutdown with resume support

## Testing Strategy

- Parser tests with sample HTML fixtures
- Downloader tests with mock HTTP server
- Integration tests for full workflows
- Mock sites for reliable testing

## Config File (~/.scraper.yaml)

```yaml
output_dir: ~/Downloads
concurrent_downloads: 10
timeout: 30s
user_agent: "Scraper/1.0"
```