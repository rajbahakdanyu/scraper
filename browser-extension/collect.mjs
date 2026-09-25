// Supported domains, mirroring siteDomains in cmd/root.go. Keep in sync when
// the scraper adds or removes a mirror. Host matching follows detectSite:
// exact hostname match or a true subdomain.
export const SUPPORTED_DOMAINS = {
  youtube: ["youtube.com", "youtu.be"],
  cyberdrop: ["cyberdrop.cr", "cyberdrop.me", "cyberdrop.to", "cyberdrop.cc"],
  bunker: [
    "bunkr.cr", "bunkr.su", "bunkrr.su", "bunkr.si", "bunkr.la",
    "bunkr.is", "bunkr.to", "bunkr.ru", "bunkr.black", "bunkr.red",
    "bunkr.ph", "bunkr.site", "bunkr.fi", "bunkr.pk", "bunkr.ac",
  ],
  goonbox: ["goonbox.cr", "goonbox.to"],
  filester: ["filester.sh", "filester.me"],
  pixeldrain: ["pixeldrain.com"],
  turbo: ["turbo.cr"],
};

const DOMAIN_TO_SITE = new Map();
for (const [site, domains] of Object.entries(SUPPORTED_DOMAINS)) {
  for (const domain of domains) DOMAIN_TO_SITE.set(domain, site);
}

// matchSite returns the scraper site name for a hostname, or null when the
// host is not one of the supported domains (exact or subdomain).
export function matchSite(hostname) {
  if (typeof hostname !== "string" || hostname === "") return null;
  const host = hostname.toLowerCase();
  const exact = DOMAIN_TO_SITE.get(host);
  if (exact) return exact;
  for (const [domain, site] of DOMAIN_TO_SITE) {
    if (host.endsWith("." + domain)) return site;
  }
  return null;
}

// collectGroups filters browser tab objects down to supported http(s) URLs,
// dedupes exact URLs, orders them by (windowId, index), and splits them into
// YouTube and everything-else groups. skipped counts tabs rejected for a
// missing URL, a non-http(s) scheme, or an unsupported host; duplicates are
// silently deduped and are not counted as skipped.
export function collectGroups(tabs) {
  const seen = new Set();
  const kept = [];
  let skipped = 0;

  const sorted = [...tabs].sort(
    (a, b) => (a?.windowId ?? 0) - (b?.windowId ?? 0) || (a?.index ?? 0) - (b?.index ?? 0),
  );

  for (const tab of sorted) {
    const raw = tab?.url;
    if (!raw) {
      skipped++;
      continue;
    }
    let url;
    try {
      url = new URL(raw);
    } catch {
      skipped++;
      continue;
    }
    if (url.protocol !== "http:" && url.protocol !== "https:") {
      skipped++;
      continue;
    }
    const site = matchSite(url.hostname);
    if (!site) {
      skipped++;
      continue;
    }
    if (seen.has(raw)) continue;
    seen.add(raw);
    kept.push({ site, url: raw });
  }

  const youtube = [];
  const other = [];
  for (const { site, url } of kept) {
    if (site === "youtube") youtube.push(url);
    else other.push(url);
  }
  return { youtube, other, skipped };
}

// formatTxt renders a URL list as the one-URL-per-line format the scraper's
// --input-file reader expects, with a trailing newline.
export function formatTxt(urls) {
  if (urls.length === 0) return "";
  return urls.join("\n") + "\n";
}
