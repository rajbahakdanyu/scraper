---
title: Retrying failures
description: Replay failed downloads into their original album folders.
---

Transient failures are retried during the run with exponential backoff (five
attempts, honoring `Retry-After` when sent). Signed URLs are re-resolved once
when an attempt fails, so most blips recover on their own.

## Retry lists

When a run ends with failures, a retry list named
`scraper-failed-<date>-<time>.txt` is written to the output directory, with a
summary grouped by cause:

```
Failed 91 item(s):
  86 × HTTP 503 from pc9.scdn.st
  5 × connection dropped
Retry list: ~/Downloads/scraper-failed-20260926-060900.txt
```

Each line pairs the failed item's page URL with its album, separated by a tab:

```
https://bunkr.cr/f/5RWka6Gp2cjOv	https://bunkr.cr/a/rBSKhzaL
```

Replaying the list scrapes only those items and saves them into their
original album folder, next to the rest of the album:

```bash
scraper --input-file ~/Downloads/scraper-failed-20260926-060900.txt
```

:::tip
Many 503s from one host mean it's overloaded. Retry with `--per-host 1`.
:::

Lines with a single URL, such as hand-written lists or
[extension exports](/guides/browser-extension/), still work and
download to that URL's own folder. Items without a page of their own (such
as GoonBox's direct image links) are written as their album URL alone;
replaying re-scrapes the album and downloads only the missing items.

From the dashboard summary, press <kbd>L</kbd> to write the list on demand.
In [JSON mode](/guides/scripting/#json-lines) the path is reported as
`retryFile` in the final `summary` event.

## Run log

`--log-file` appends a timestamped log of scrape errors and retry summaries,
which is useful for unattended or [watch](/guides/batch-downloads/#watch-mode)
runs.
