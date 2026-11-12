---
title: Dashboard
description: Navigate, filter, cancel, and retry from the terminal dashboard.
---

Every command opens the terminal dashboard immediately, unless `--dry-run`,
`--no-tui`, or `--json` is set. Scraping runs in the background behind a
spinner, then the download view appears.

Progress and statistics always cover the whole run, including items hidden
by a filter or a folded album. Completed albums fold automatically; albums
with failures stay open, and albums you unfold stay open. While a file's size
is unknown, the overall byte percentage is marked with `~`.

## Keys

Keys are case-sensitive.

| Key | Action |
| --- | --- |
| <kbd>↑</kbd>/<kbd>↓</kbd>, <kbd>j</kbd>/<kbd>k</kbd>, mouse wheel | Select an item or folded album |
| <kbd>c</kbd> | Cancel the selected queued or active download |
| <kbd>C</kbd> | Cancel all queued and active downloads |
| <kbd>r</kbd> | Retry the selected failed download |
| <kbd>R</kbd> | Retry all failures; from the summary, return to the download list |
| <kbd>F</kbd> | Jump to the next failed item |
| <kbd>/</kbd> | Search titles; <kbd>enter</kbd> accepts and <kbd>esc</kbd> clears |
| <kbd>n</kbd> | Jump to the next search match |
| <kbd>enter</kbd> | Show the selected failed item's error; from the summary, view the file list |
| <kbd>f</kbd> | Cycle all, failed, active, and pending items |
| <kbd>Tab</kbd> | Fold or unfold the current album |
| <kbd>e</kbd> | Show scrape errors |
| <kbd>L</kbd> | Write the retry list from the end-of-run summary |
| <kbd>q</kbd>, <kbd>esc</kbd>, <kbd>Ctrl</kbd>+<kbd>C</kbd> | Quit (outside search) |

## Summary

The summary appears once every album has been scraped and every download has
finished. It reports completed, failed, and cancelled counts, and accepts
only <kbd>enter</kbd>, <kbd>R</kbd>, <kbd>L</kbd>, and the quit keys.
Retrying from the file list brings the summary back when the retried
downloads finish.
