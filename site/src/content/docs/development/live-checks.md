---
title: Live site checks
description: Catch site changes by downloading one real item per site.
---

The offline tests use fake servers, so they can't notice when a site changes
its pages or API. `internal/livetest` downloads one real item per site. It
only builds with `-tags live`, and only checks sites whose `LIVE_URL_<SITE>`
variable is set: `BUNKER`, `CYBERDROP`, `FILESTER`, `GOONBOX`, `PIXELDRAIN`,
`TURBO`, or `YOUTUBE`. Several URLs may be separated by spaces or commas.

```bash
LIVE_URL_PIXELDRAIN=https://pixeldrain.com/u/<id> \
LIVE_URL_YOUTUBE='https://www.youtube.com/watch?v=<id>' \
go test -tags live -v ./internal/livetest
```

It picks each album's smallest item up to `LIVE_MAX_BYTES` (default 200 MiB)
and fetches YouTube at 360p.

The **Live site checks** workflow runs nightly and on demand, using the same
names set as repository **variables**. A failed nightly run opens, or
comments on, a `live-check` issue.
