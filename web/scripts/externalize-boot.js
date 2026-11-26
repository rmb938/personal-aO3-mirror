// SvelteKit writes its start-up code as an inline <script> in the page, which the UI's CSP
// (`script-src 'self'`, internal/web/ui) blocks. This adapter wrapper moves that script into a
// content-hashed file next to the other immutable assets after the build, then checks that the
// page has no inline script, inline style or event handler left (docs/part4-ui.md, stage 3).
//
// The boot script finds its mount point with `document.currentScript`, so it must stay a
// classic script (not type="module") in the same place in the page.

import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';

// A script element. Its end tag is matched as the HTML tokenizer ends script data: `</script`
// (any case) followed by whitespace, `/` or `>`, then anything up to the next `>`, so
// `</script >`, `</script\n x>` and `</SCRIPT/>` all end it and none is missed.
const SCRIPT = /<script\b([^>]*)>([\s\S]*?)<\/script(?=[\t\n\f\r />])[^>]*>/gi;

/**
 * Moves the page's one inline script out to `${urlDir}/boot.<hash>.js`.
 * @param {string} html the built page
 * @param {string} urlDir the URL directory the script is served from (no trailing slash)
 * @returns {{ html: string, name: string, script: string }} the new page, the script's file
 *   name and its contents
 */
export function externalizeBoot(html, urlDir) {
	const inline = [...html.matchAll(SCRIPT)].filter((m) => !/\bsrc\s*=/i.test(m[1] ?? ''));
	const [first, ...rest] = inline;
	if (first === undefined || rest.length > 0) {
		throw new Error(`externalize-boot: expected one inline script, found ${String(inline.length)}`);
	}
	const [whole, attrs = '', script = ''] = first;
	if (attrs.trim() !== '') {
		throw new Error('externalize-boot: the inline script has attributes; expected a bare <script>');
	}
	const hash = createHash('sha256').update(script).digest('hex').slice(0, 16);
	const name = `boot.${hash}.js`;
	const out = html.replace(whole, () => `<script src="${urlDir}/${name}"></script>`);
	assertNoInline(out);
	return { html: out, name, script };
}

/**
 * Throws unless html has no inline script, <style> element, style attribute or on* handler:
 * the CSP would block any of them.
 * @param {string} html
 */
export function assertNoInline(html) {
	for (const m of html.matchAll(SCRIPT)) {
		if (!/\bsrc\s*=/i.test(m[1] ?? '')) {
			throw new Error('externalize-boot: the page still has an inline script');
		}
	}
	if (/<style\b/i.test(html)) {
		throw new Error('externalize-boot: the page has a <style> element');
	}
	if (/<[^>]*\sstyle\s*=/i.test(html)) {
		throw new Error('externalize-boot: the page has a style attribute');
	}
	if (/<[^>]*\son[a-z]+\s*=/i.test(html)) {
		throw new Error('externalize-boot: the page has an inline event handler');
	}
}

/**
 * Wraps a SvelteKit adapter that writes a fallback page (adapter-static) so the page's boot
 * script is moved to a file after the adapter runs.
 * @param {import('@sveltejs/kit').Adapter} inner
 * @param {{ pages: string, fallback: string }} out where inner writes the build and the page
 * @returns {import('@sveltejs/kit').Adapter}
 */
export function withExternalBoot(inner, out) {
	return {
		...inner,
		name: `${inner.name}+externalize-boot`,
		async adapt(builder) {
			await inner.adapt(builder);
			const { appDir, paths } = builder.config;
			const page = path.join(out.pages, out.fallback);
			const result = externalizeBoot(readFileSync(page, 'utf8'), `${paths.base}/${appDir}/immutable`);
			writeFileSync(path.join(out.pages, appDir, 'immutable', result.name), result.script);
			writeFileSync(page, result.html);
			builder.log.minor(`Moved the boot script to ${appDir}/immutable/${result.name}`);
		}
	};
}
