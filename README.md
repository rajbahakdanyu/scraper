# Scraper CLI

A Go-based CLI tool for scraping and downloading media from YouTube,
CyberDrop, Bunker, GoonBox, and Filester — with a futuristic terminal UI that
launches instantly and shows live download progress.

## Requirements

- Go 1.21 or newer
- `ffmpeg` (for YouTube downloads above 360p — video+audio are muxed automatically)

## Build

```bash
go build -o scraper ./cmd/scraper
```

## Usage

Every command launches the TUI immediately. The scraper runs in the
background with a loading spinner, then transitions to the download view.

The site can be auto-detected from the URL — just pass a bare URL with no
subcommand:

```bash
./scraper https://youtu.be/VIDEO_ID
./scraper https://filester.sh/f/FOLDER_ID
```

Or use an explicit subcommand (useful for scripting, or if detection ever
guesses wrong):

```bash
./scraper youtube https://youtu.be/VIDEO_ID
./scraper cyberdrop https://cyberdrop.cr/f/FILE_ID
./scraper bunker https://bunkr.cr/a/ALBUM_ID
./scraper goonbox https://goonbox.cr/a/ALBUM_ID
./scraper filester https://filester.sh/f/FOLDER_ID
./scraper filester https://filester.sh/d/FILE_SLUG
```

### YouTube

Shows a quality selector (144p–2160p). Pick a resolution and press Enter
to download. For resolutions above 360p, video and audio streams are
downloaded separately and muxed with ffmpeg.

### Albums (Bunker, GoonBox, CyberDrop, Filester)

All files in an album/folder are auto-downloaded concurrently into a
subfolder named after the album ID:

```
./downloads/
  k4OuX4pJ/        ← album subfolder
    image1.png
    video1.mp4
    ...
```

Single files (e.g. CyberDrop `/f/` links or Filester `/d/` links) download
directly to the output directory.

### Flags

| Flag             | Default | Description                                  |
|------------------|---------|-----------------------------------------------|
| `-u, --url`      | (none)  | Content URL (alternative to positional arg)  |
| `-o, --output`   | `.`     | Output directory                             |
| `--concurrent`   | `5`     | Maximum concurrent downloads                 |
| `--config`       | (none)  | Config file path (default: `~/.scraper.yaml`)|

### TUI Keybindings

| Key             | Action                           |
|-----------------|-----------------------------------|
| `↑/k`, `↓/j`    | Navigate / select quality        |
| `enter`         | Start download (YouTube)         |
| `c`             | Cancel the selected item (batch downloads) |
| `q`, `ctrl+c`   | Quit                              |

## Configuration

A YAML config file can be placed at `~/.scraper.yaml`:

```yaml
output_dir: ~/Downloads
concurrent_downloads: 10
timeout: 30s
user_agent: "Scraper/1.0"
```

CLI flags override config file values.

## Supported Sites

| Site      | URL format                          | Notes                                |
|-----------|-------------------------------------|--------------------------------------|
| YouTube   | `youtu.be/ID` or `youtube.com/watch?v=ID` | Quality selector + ffmpeg muxing |
| CyberDrop | `cyberdrop.cr/f/SLUG` or `/a/SLUG` | API-based, signed CDN URLs           |
| Bunker    | `bunkr.cr/a/ALBUM_ID`              | API + token signing, parallel fetch  |
| GoonBox   | `goonbox.cr/a/ALBUM_ID`            | JSON API, paginated albums           |
| Filester  | `filester.sh/f/ID` or `/d/SLUG`    | HTML folder listing + signed download tokens, paginated |

## Development

```bash
go build ./...
go vet ./...
go test ./...
```
