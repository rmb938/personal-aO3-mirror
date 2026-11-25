import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vitest/config';

import { withExternalBoot } from './scripts/externalize-boot.js';
import { noInlineStyles } from './scripts/no-inline-styles.js';

// A static single-page app (docs/part4-ui.md): `make ui` copies build/ into internal/web/ui/dist,
// which `ao3mirror serve` embeds. Every app route gets index.html (the fallback page).
const out = { pages: 'build', fallback: 'index.html' };

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
		include: ['src/**/*.test.ts', 'scripts/**/*.test.js'],
		environment: 'node'
	}
});
