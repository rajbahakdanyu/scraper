#!/usr/bin/env node
// Groups Conventional Commits into release notes. Used by the release
// workflow for each release's notes and by the docs site for its changelog.
//
//   node .github/scripts/changelog.mjs notes <previous-tag|""> <rev> <tag>
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

export const REPO_URL = 'https://github.com/rajbahakdanyu/scraper';

const SECTIONS = [
	{ key: 'breaking', title: '⚠️ Breaking changes' },
	{ key: 'feat', title: 'Features' },
	{ key: 'fix', title: 'Fixes' },
	{ key: 'perf', title: 'Performance' },
	{ key: 'other', title: 'Other changes', collapsed: true },
];
const SUBJECT = /^(?<type>[A-Za-z]+)(?:\((?<scope>[^)]*)\))?(?<bang>!)?:\s*(?<text>.+)$/;
const SEMVER_TAG = /^v\d+\.\d+\.\d+$/;

function git(cwd, ...args) {
	return execFileSync('git', args, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
}

/** vX.Y.Z tags, newest first, with the date of the commit they point to. */
export function releaseTags(cwd) {
	return git(cwd, 'tag', '--list', 'v*', '--sort=-v:refname', '--format=%(refname:short)%09%(*committerdate:short)%(committerdate:short)')
		.split('\n')
		.map((line) => line.split('\t'))
		.filter(([tag]) => SEMVER_TAG.test(tag ?? ''))
		.map(([tag, date]) => ({ tag, date }));
}

export function commits(cwd, from, to) {
	const range = from ? `${from}..${to}` : to;
	return git(cwd, 'log', '--no-merges', '--format=%H%x1f%s%x1f%b%x1e', range)
		.split('\x1e')
		.map((entry) => entry.trim())
		.filter(Boolean)
		.map((entry) => {
			const [hash, subject, body = ''] = entry.split('\x1f');
			return { hash, subject, body };
		});
}

function classify({ hash, subject, body }) {
	const match = SUBJECT.exec(subject);
	const breaking = Boolean(match?.groups.bang) || /^BREAKING[ -]CHANGE:/m.test(body);
	const type = match?.groups.type.toLowerCase();
	const key = breaking ? 'breaking' : ['feat', 'fix', 'perf'].includes(type) ? type : 'other';
	const text = match ? match.groups.text : subject;
	return {
		key,
		hash,
		scope: match?.groups.scope?.trim() || undefined,
		text: text.charAt(0).toUpperCase() + text.slice(1),
	};
}

function escape(text) {
	return text.replace(/[<>]/g, (c) => (c === '<' ? '&lt;' : '&gt;'));
}

function item({ hash, scope, text }) {
	const prefix = scope ? `**${escape(scope)}:** ` : '';
	return `- ${prefix}${escape(text)} ([\`${hash.slice(0, 7)}\`](${REPO_URL}/commit/${hash}))`;
}

/** Markdown for the commits in previous..rev, grouped by type. */
export function notes(cwd, previous, rev, { headingLevel = 3 } = {}) {
	const entries = commits(cwd, previous, rev).map(classify);
	const hashes = '#'.repeat(headingLevel);
	const out = [];
	for (const { key, title, collapsed } of SECTIONS) {
		const items = entries.filter((entry) => entry.key === key).map(item);
		if (items.length === 0) continue;
		if (collapsed) {
			out.push(`<details>\n<summary>${title} (${items.length})</summary>\n\n${items.join('\n')}\n\n</details>`);
		} else {
			out.push(`${hashes} ${title}\n\n${items.join('\n')}`);
		}
	}
	if (out.length === 0) out.push('No changes.');
	return out.join('\n\n');
}

export function compareLink(previous, tag) {
	return previous
		? `**Full changelog:** [${previous}...${tag}](${REPO_URL}/compare/${previous}...${tag})`
		: `**Full changelog:** [${tag}](${REPO_URL}/commits/${tag})`;
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) {
	const [command, previous, rev, tag] = process.argv.slice(2);
	if (command !== 'notes' || !rev || !tag) {
		console.error('usage: changelog.mjs notes <previous-tag|""> <rev> <tag>');
		process.exit(2);
	}
	console.log(`${notes(process.cwd(), previous, rev)}\n\n${compareLink(previous, tag)}`);
}
