import test from "node:test";
import assert from "node:assert/strict";

import { SUPPORTED_DOMAINS, matchSite, collectGroups, formatTxt } from "../collect.mjs";

test("matchSite accepts exact domains and true subdomains", () => {
  assert.equal(matchSite("youtube.com"), "youtube");
  assert.equal(matchSite("www.youtube.com"), "youtube");
  assert.equal(matchSite("bunkr.su"), "bunker");
  assert.equal(matchSite("cdn.bunkr.su"), "bunker");
  assert.equal(matchSite("pixeldrain.com"), "pixeldrain");
});

test("matchSite is case-insensitive and rejects lookalikes", () => {
  assert.equal(matchSite("WWW.YouTube.COM"), "youtube");
  assert.equal(matchSite("notyoutube.com"), null);
  assert.equal(matchSite("youtube.com.evil.example"), null);
  assert.equal(matchSite("evil.example"), null);
  assert.equal(matchSite(""), null);
  assert.equal(matchSite(undefined), null);
});

test("SUPPORTED_DOMAINS mirrors the scraper's site list", () => {
  assert.deepEqual(Object.keys(SUPPORTED_DOMAINS).sort(), [
    "bunker", "cyberdrop", "filester", "goonbox", "pixeldrain", "turbo", "youtube",
  ]);
  assert.equal(SUPPORTED_DOMAINS.youtube.length, 2);
  assert.ok(SUPPORTED_DOMAINS.bunker.includes("bunkr.cr"));
  assert.equal(SUPPORTED_DOMAINS.bunker.length, 15);
});

const tab = (url, windowId = 0, index = 0) => ({ url, windowId, index });

test("collectGroups splits YouTube from other supported tabs", () => {
  const groups = collectGroups([
    tab("https://youtube.com/watch?v=abc"),
    tab("https://bunkr.cr/a/xyz"),
    tab("https://youtu.be/def"),
    tab("https://filester.sh/f/1"),
  ]);
  assert.deepEqual(groups.youtube, ["https://youtube.com/watch?v=abc", "https://youtu.be/def"]);
  assert.deepEqual(groups.other, ["https://bunkr.cr/a/xyz", "https://filester.sh/f/1"]);
  assert.equal(groups.skipped, 0);
});

test("collectGroups skips unsupported hosts, odd schemes, and missing URLs", () => {
  const groups = collectGroups([
    tab("https://google.com/search"),
    tab("about:blank"),
    tab("file:///etc/hosts"),
    tab("moz-extension://abc/page.html"),
    tab(undefined),
    tab("https://youtube.com/watch?v=keep"),
  ]);
  assert.deepEqual(groups.youtube, ["https://youtube.com/watch?v=keep"]);
  assert.deepEqual(groups.other, []);
  assert.equal(groups.skipped, 5);
});

test("collectGroups dedupes and does not count duplicates as skipped", () => {
  const groups = collectGroups([
    tab("https://youtube.com/watch?v=dup", 0, 0),
    tab("https://youtube.com/watch?v=dup", 1, 3),
    tab("https://youtube.com/watch?v=dup", 0, 1),
  ]);
  assert.deepEqual(groups.youtube, ["https://youtube.com/watch?v=dup"]);
  assert.equal(groups.skipped, 0);
});

test("collectGroups orders deterministically by (windowId, index)", () => {
  const groups = collectGroups([
    tab("https://bunkr.cr/third", 1, 0),
    tab("https://bunkr.cr/second", 0, 5),
    tab("https://bunkr.cr/first", 0, 2),
  ]);
  assert.deepEqual(groups.other, [
    "https://bunkr.cr/first",
    "https://bunkr.cr/second",
    "https://bunkr.cr/third",
  ]);
});

test("collectGroups handles uppercase hosts, ports, and empty input", () => {
  const groups = collectGroups([
    tab("HTTPS://WWW.YouTube.COM:443/watch?v=u"),
  ]);
  assert.deepEqual(groups.youtube, ["HTTPS://WWW.YouTube.COM:443/watch?v=u"]);
  assert.deepEqual(collectGroups([]), { youtube: [], other: [], skipped: 0 });
});

test("formatTxt writes one URL per line with a trailing newline", () => {
  assert.equal(formatTxt(["https://a.example", "https://b.example"]), "https://a.example\nhttps://b.example\n");
  assert.equal(formatTxt([]), "");
});
