---
title: Contributing
description: Build and test the CLI, desktop app, and docs.
---

## CLI

The CLI is a CGO-free Go module at the repository root (Go 1.26.11+):

```bash
go build ./...
go vet ./...
go test ./...
```

Shared code lives in `internal/`:

- `internal/app`: site detection, filters, previews, downloaders, retry lists
- `internal/session`: the download-run state machine that both the dashboard
  and the desktop app drive
- `internal/config`: loading and saving `~/.scraper.yaml`

## Desktop app

The desktop app is a separate Go module in `gui/`, so the CLI stays CGO-free.
You need:

- Node.js `^22.22.2 || ^24.15.0 || >=26.0.0` (`gui/frontend/.nvmrc` pins 26)
- [pnpm](https://pnpm.io/installation), at the version pinned by
  `packageManager` in `gui/frontend/package.json`
- The [Wails v3.1.0 CLI](https://v3.wails.io/getting-started/installation)
- On Linux, `libgtk-4-dev` and `libwebkitgtk-6.0-dev`

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
cd gui
WAILS_EXP_USE_WAKE=1 wails3 build   # app in gui/build/bin/
```

`WAILS_EXP_USE_WAKE=1` enables Wails 3.1's YAML build pipeline, which the
project uses.

`WAILS_EXP_USE_WAKE=1 wails3 dev` (in `gui/`) runs the app with hot reload
and also serves the UI at <http://localhost:34115> with live Go bindings. The
Vite-only URL (usually port 5173) has no bindings, so site detection and
downloads don't work there.

Frontend checks and Go tests:

```bash
cd gui/frontend && pnpm install --frozen-lockfile && pnpm run check && pnpm test && pnpm run build
cd .. && go test ./...
```

After changing the backend API exposed to the frontend, regenerate the
TypeScript bindings (CI fails if they're stale):

```bash
cd gui
wails3 generate bindings -ts -d frontend/bindings -dir . ./backend
```

## Browser extension

```bash
node --test 'browser-extension/tests/*.test.mjs'
```

The supported-domain list in `browser-extension/collect.mjs` mirrors
`siteDomains` in `cmd/root.go`; update both together.

## Docs

This site is built with [Starlight](https://starlight.astro.build) from
`site/` and deployed to GitHub Pages when `site/` changes on `main`.

```bash
cd site
pnpm install --frozen-lockfile
pnpm dev     # http://localhost:4321/scraper/
pnpm check
pnpm build
```

Pages live in `site/src/content/docs/`; add new ones to the sidebar in
`site/astro.config.mjs`.
