import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

test("manifest is valid MV3 and requests only what the popup needs", () => {
  const manifest = JSON.parse(readFileSync(join(root, "manifest.json"), "utf8"));
  assert.equal(manifest.manifest_version, 3);
  assert.deepEqual(manifest.permissions, ["tabs", "downloads"]);
  assert.equal(manifest.action.default_popup, "popup.html");
  // Downloads are orchestrated from the background page so they survive
  // the popup closing; it must be a module to import collect.mjs.
  assert.deepEqual(manifest.background.scripts, ["background.mjs"]);
  assert.equal(manifest.background.type, "module");
  // data_collection_permissions is only supported from Firefox 140, and
  // Firefox for Android 142.
  assert.match(manifest.browser_specific_settings.gecko.strict_min_version, /^142\./);
  // Required for new Firefox extensions; the add-on collects nothing.
  assert.deepEqual(manifest.browser_specific_settings.gecko.data_collection_permissions, {
    required: ["none"],
  });
});

test("every file referenced by the manifest exists", () => {
  const manifest = JSON.parse(readFileSync(join(root, "manifest.json"), "utf8"));
  for (const relative of [manifest.action.default_popup, ...manifest.background.scripts]) {
    assert.ok(existsSync(join(root, relative)), `${relative} is missing`);
  }
});

test("popup.html references popup.mjs as a module and an export button", () => {
  const html = readFileSync(join(root, "popup.html"), "utf8");
  assert.match(html, /<script type="module" src="popup\.mjs">/);
  assert.match(html, /id="export"/);
});
