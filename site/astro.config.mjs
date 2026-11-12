// @ts-check
import { defineConfig } from 'astro/config';
import { readFileSync } from 'node:fs';
import starlight from '@astrojs/starlight';

const repo = 'https://github.com/rajbahakdanyu/scraper';

export default defineConfig({
	site: 'https://rajbahakdanyu.github.io',
	base: '/scraper',
	trailingSlash: 'always',
	integrations: [
		starlight({
			title: 'Scraper',
			description:
				'Download media from YouTube, CyberDrop, Bunkr, GoonBox, Filester, Pixeldrain, and Turbo from the terminal or a desktop app.',
			logo: { src: './src/assets/logo.svg' },
			favicon: '/favicon.svg',
			social: [{ icon: 'github', label: 'GitHub', href: repo }],
			editLink: { baseUrl: `${repo}/edit/main/site/` },
			lastUpdated: true,
			customCss: ['./src/styles/custom.css'],
			head: [
				{
					tag: 'script',
					content: readFileSync(new URL('./src/scripts/header.js', import.meta.url), 'utf8'),
				},
			],
			sidebar: [
				{
					label: 'Getting started',
					items: ['getting-started/installation', 'getting-started/quick-start'],
				},
				{
					label: 'Guides',
					items: [
						'guides/desktop-app',
						'guides/batch-downloads',
						'guides/dashboard',
						'guides/youtube',
						'guides/albums',
						'guides/filtering',
						'guides/retrying',
						'guides/scripting',
						'guides/browser-extension',
					],
				},
				{
					label: 'Reference',
					items: ['reference/sites', 'reference/flags', 'reference/configuration', 'changelog'],
				},
				{
					label: 'Development',
					items: ['development/contributing', 'development/live-checks', 'development/releasing'],
				},
			],
		}),
	],
});
