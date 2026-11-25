import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import { findTemplateStyles, stripAnnouncerStyle } from './no-inline-styles.js';

/** Kit's real root.svelte, so a Kit update that moves the announcer fails here first. */
function kitRoot() {
	const kitPkg = createRequire(import.meta.url).resolve('@sveltejs/kit/package.json');
	return readFileSync(path.join(path.dirname(kitPkg), 'src/runtime/components/root.svelte'), 'utf8');
}

describe('stripAnnouncerStyle', () => {
	it("removes only the announcer's style attribute from Kit's root.svelte", () => {
		const before = kitRoot();
		const after = stripAnnouncerStyle(before);
		expect(after).not.toMatch(/\sstyle=/);
		expect(after).toContain('id="svelte-announcer"');
		expect(after).toContain('aria-live="assertive"');
		expect(before.length - after.length).toBeGreaterThan(0);
		expect(after.split('\n').length).toBe(before.split('\n').length - 1);
	});

	it('handles the attribute on the same line', () => {
		expect(stripAnnouncerStyle('<div id="svelte-announcer" aria-live="assertive" style="a: b">x</div>')).toBe(
			'<div id="svelte-announcer" aria-live="assertive">x</div>'
		);
	});

	it.each([
		['no announcer', '<div id="other" style="a: b"></div>'],
		['an announcer without a style', '<div id="svelte-announcer"></div>'],
		['two announcers', '<div id="svelte-announcer" style="a"></div><div id="svelte-announcer" style="b"></div>']
	])('fails on %s', (_, code) => {
		expect(() => stripAnnouncerStyle(code)).toThrow(/expected one styled #svelte-announcer/);
	});
});

describe('findTemplateStyles', () => {
	it('finds a style attribute in a compiled template string', () => {
		const chunk = 'var root = from_html(`<div id="a" style="position: absolute">x</div>`);';
		expect(findTemplateStyles(chunk)).toEqual(['<div id="a" style="position: absolute">']);
	});

	it('finds single-quoted and spaced attributes', () => {
		expect(findTemplateStyles("t('<p class=x style = \\'a\\'>')")).toHaveLength(1);
	});

	it.each([
		['the style property', 'el.style.setProperty("color", "red")'],
		['a style element name', 'document.createElement("style")'],
		['a class mentioning style', '`<div class="styled">`'],
		['a data attribute', '`<div data-style="x">`']
	])('ignores %s', (_, chunk) => {
		expect(findTemplateStyles(chunk)).toEqual([]);
	});
});
