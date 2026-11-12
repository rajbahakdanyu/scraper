---
title: Desktop app
description: Use Scraper from a native window on macOS, Windows, or Linux.
---

`scraper-gui` is a desktop app built with [Wails](https://wails.io) and
Svelte on the same scraping, download, and run-state code as the terminal
dashboard. Download it from the
[latest release](/getting-started/installation/#download-a-release).

It reads and writes the same [`~/.scraper.yaml`](/reference/configuration/)
as the CLI. Settings can apply to the next run only, or be saved to the file;
comments and unknown keys in the file are kept.

## CLI equivalents

| Terminal | Desktop app |
| --- | --- |
| `scraper <url>`, per-site subcommands | Paste URLs; **Site** forces the site for a single URL |
| Several URLs, `-i/--input-file`, stdin, retry lists | Paste several URLs or **Import list…** (always runs as a batch) |
| `--dry-run` | **Preview** (items, sizes, unknown sizes, disk warning) |
| `--watch` | **Watch every** interval, **Stop watching** |
| Flags and config keys | **Settings** (every config key) |
| Dashboard: progress, stats, speed, ETA, album headers | Run view with the same totals and a grouped, foldable list |
| `c` `C` `r` `R` `F` `/` `n` `f` `Tab` `e` `enter` | Same keys, plus row buttons and filter chips |
| Summary: `enter`, `R`, `L`, `q` | Summary dialog with the same actions |
| Retry list written on exit | Written when each group closes; **Show** reveals it |
| Warnings after the dashboard exits | **Notices** panel |
| `--notify`, `--log-file` | Same settings |

The app also opens the output folder, reveals finished files, and copies
item URLs.

## Native behavior

- Only one copy runs at a time; launching it again focuses the open window.
- On macOS it has the standard app and Edit menus plus a Window menu.
- <kbd>⌘</kbd>/<kbd>Ctrl</kbd>+<kbd>1</kbd>–<kbd>3</kbd> switch between
  Download, Run, and Settings; <kbd>⌘</kbd>/<kbd>Ctrl</kbd>+<kbd>,</kbd> opens
  Settings.
- Browser shortcuts such as reload and print are disabled.

## Watching in the background

While a watch is active, closing the window asks whether to quit (stopping
the watch) or hide the window and keep watching. Relaunch the app to bring a
hidden window back, or choose **Window → Show Scraper** on macOS. Intervals
missed while the computer slept are picked up within a minute of waking.
