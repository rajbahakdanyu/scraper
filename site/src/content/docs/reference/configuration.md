---
title: Configuration
description: Set defaults in ~/.scraper.yaml.
---

Defaults live in `~/.scraper.yaml` (or the file given with `--config`).
[Flags](/reference/flags/) override it. The
[desktop app](/guides/desktop-app/) reads and writes the same file and
keeps your comments and unknown keys.

```yaml title="~/.scraper.yaml"
output_dir: ~/Downloads
concurrent_downloads: 10
per_host_concurrency: 2   # per server; 0 = no limit
timeout: 30s
user_agent: "Scraper/1.0"
rate_limit: 2M
checksums: false
proxy: http://proxy:8080
cookies: ~/.scraper-cookies.txt
quality: "1080p"
audio_only: ""            # off; or "m4a" / "mp3"
youtube_backend: auto     # or "builtin" / "yt-dlp"
include: ["*.mp4", "*.mkv"]
exclude: ["*.txt"]
media_extensions: []
max_album_items: 10000
scrape_delay: 0s
log_file: ""
min_size: "1M"
max_size: "10G"
disk_check: true
media_only: true
notify: false
```

:::caution
`audio_only` must be a string. A YAML boolean such as `true` fails to load.
:::

## Notes

- `proxy` routes every request, both scraping and downloads, through the
  given HTTP/S proxy.
- `rate_limit` caps the combined speed of all downloads, not each one.
- `per_host_concurrency` limits downloads to the same server. When a server
  answers 429 or 503, all downloads from it pause for the `Retry-After` time.
- `checksums` is off by default; existing files are then checked by size
  only. Turn it on to write `.sha256` sidecars and re-download files that no
  longer match.
