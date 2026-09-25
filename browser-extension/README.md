# Scraper Tab Export (Firefox)

One-click export of every open tab on a [scraper](../README.md)-supported
domain as `--input-file` lists in your Downloads folder.

## Install (temporary add-on)

1. Open `about:debugging#/runtime/this-firefox`.
2. Click **Load Temporary Add-on…**.
3. Select `browser-extension/manifest.json`.

The add-on stays loaded until Firefox restarts. This is fine for personal
use; there is no build step and no packaging.

## Use

1. Open your album/file tabs (any windows).
2. Click the extension button → **Export supported tabs** and wait for the
   status line (usually well under a second). The export runs in the
   extension's background page, so it continues even if you close the popup.
3. Downloads appear in your Downloads folder:

   | File | Contents |
   |---|---|
   | `scraper-tabs.txt` | all supported non-YouTube tabs |
   | `scraper-youtube.txt` | YouTube tabs (exported separately because the scraper cannot batch YouTube and non-YouTube URLs together) |

   Only a file whose group has tabs is created. Unsupported tabs (search
   engines, `about:`, etc.) are counted and skipped.

4. Run the scraper:

   ```bash
   scraper --dry-run --input-file ~/Downloads/scraper-tabs.txt
   scraper --input-file ~/Downloads/scraper-tabs.txt
   scraper --input-file ~/Downloads/scraper-youtube.txt
   ```

## Notes

- Tabs in private windows are exported only if you allow the extension in
  private browsing.
- The supported-domain list lives in `collect.mjs` and mirrors
  `siteDomains` in `../cmd/root.go`; update both together when a mirror
  changes.
- Run the tests with `node --test 'browser-extension/tests/*.test.mjs'`.
