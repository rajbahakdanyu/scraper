---
title: Scripting
description: Plain text and JSON output, dry runs, and exit codes.
---

## Plain text

`--no-tui` turns off the dashboard and prints a line as each item is scraped
and downloaded:

```bash
scraper --no-tui https://youtu.be/VIDEO_ID
```

## JSON Lines

`-j`/`--json` prints one JSON event per line and implies `--no-tui`. Event
types:

| Event | Meaning |
| --- | --- |
| `album_start` | An album began scraping |
| `album_error` | An album failed to scrape |
| `scrape_warning` | A scrape returned partial results |
| `item` | An item's progress or result |
| `summary` | Final totals; includes `retryFile` and `partial: true` when relevant |
| `error` | The run failed |

Combined with `--dry-run`, it prints one JSON document describing the albums
(URLs, items, and sizes) without downloading. Batches print one document per
URL.

```bash
scraper -j --dry-run https://filester.sh/f/FOLDER_ID
```

## Listing URLs

`--print-urls` prints each item's URL, one per line, and exits without
downloading.

## Exit status

The same in every mode:

| Code | Meaning |
| --- | --- |
| `0` | Everything downloaded |
| `1` | The run failed outright, e.g. the URL couldn't be scraped |
| `2` | Partial: a scrape returned partial results, or only some items or albums failed |

A partial dry run includes the items it found, reports the scrape warning,
and exits with `2`.

## Notifications

`--notify` sends a desktop notification when downloads finish.
