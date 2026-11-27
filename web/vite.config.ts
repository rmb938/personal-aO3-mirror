import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { playwright } from '@vitest/browser-playwright';
import { defineConfig } from 'vitest/config';

import { withExternalBoot } from './scripts/externalize-boot.js';
import { noInlineStyles } from './scripts/no-inline-styles.js';

// A static single-page app (docs/part4-ui.md): `make ui` copies build/ into internal/web/ui/dist,
// which `ao3mirror serve` embeds. Every app route gets index.html (the fallback page).
const out = { pages: 'build', fallback: 'index.html' };

const browserTests = 'src/**/*.browser.test.ts';

export default defineConfig({
	plugins: [
		// Before sveltekit(), so its transform sees root.svelte before Svelte compiles it.
		noInlineStyles(),
		await sveltekit({
			adapter: withExternalBoot(adapter({ ...out, strict: true }), out),
			// Absolute asset URLs, so the same index.html works at any deep link.
			paths: { relative: false }
		})
	],
	build: {
		// The CSP allows no data: URLs, so never inline assets into the bundle.
		assetsInlineLimit: 0
	},
	test: {
		projects: [
			{
				extends: true,
				test: {
					name: 'node',
					include: ['src/**/*.test.ts', 'scripts/**/*.test.js'],
					exclude: [browserTests],
					environment: 'node'
				}
			},
			{
				// Tests that need a real browser's HTML parser and Trusted Types (the sanitizers):
				// headless Chromium, the one `playwright install` puts in place for make ui-e2e.
				extends: true,
				test: {
					name: 'browser',
					include: [browserTests],
					browser: {
						enabled: true,
						headless: true,
						provider: playwright(),
						instances: [{ browser: 'chromium' }]
					}
				}
			}
		]
	}
});
