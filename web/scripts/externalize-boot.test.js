import { describe, expect, it } from 'vitest';

import { assertNoInline, externalizeBoot } from './externalize-boot.js';

/** @param {string} body */
const page = (body) =>
	`<!doctype html><html><head><link rel="modulepreload" href="/_app/immutable/entry/start.js"></head>` +
	`<body><div class="app">${body}</div></body></html>`;

describe('externalizeBoot', () => {
	it('moves the inline script to a hashed file in place', () => {
		const script = "\n{ __sveltekit_x = {}; import('/_app/immutable/entry/start.js'); }\n";
		const got = externalizeBoot(page(`<script>${script}</script>`), '/_app/immutable');
		expect(got.script).toBe(script);
		expect(got.name).toMatch(/^boot\.[0-9a-f]{16}\.js$/);
		expect(got.html).toBe(page(`<script src="/_app/immutable/${got.name}"></script>`));
	});

	it('names the file by content', () => {
		const a = externalizeBoot(page('<script>a()</script>'), '/x');
		const b = externalizeBoot(page('<script>b()</script>'), '/x');
		expect(a.name).not.toBe(b.name);
		expect(externalizeBoot(page('<script>a()</script>'), '/x').name).toBe(a.name);
	});

	it('keeps scripts that have a src', () => {
		const got = externalizeBoot(page('<script src="/a.js"></script><script>boot()</script>'), '/x');
		expect(got.html).toContain('<script src="/a.js"></script>');
		expect(got.script).toBe('boot()');
	});

	it('keeps $ sequences in the page verbatim', () => {
		const got = externalizeBoot(page("<p>$&$1</p><script>s('$&')</script>"), '/x');
		expect(got.html).toContain('<p>$&$1</p>');
		expect(got.script).toBe("s('$&')");
	});

	it.each([
		['no inline script', page('')],
		['two inline scripts', page('<script>a()</script><script>b()</script>')]
	])('rejects %s', (_, html) => {
		expect(() => externalizeBoot(html, '/x')).toThrow(/expected one inline script/);
	});

	it.each(['</script >', '</script\t\n bar>', '</SCRIPT>', '</script/>'])(
		'ends the script at %j, as a browser does',
		(end) => {
			const got = externalizeBoot(page(`<script>a()${end}`), '/x');
			expect(got.script).toBe('a()');
			expect(got.html).toBe(page('<script src="/x/' + got.name + '"></script>'));
		}
	);

	it('rejects an inline script with attributes', () => {
		expect(() => externalizeBoot(page('<script nonce="n">a()</script>'), '/x')).toThrow(/attributes/);
	});

	it('rejects a page with other inline content', () => {
		expect(() => externalizeBoot(page('<script>a()</script><style>p{}</style>'), '/x')).toThrow(/<style>/);
	});
});

describe('assertNoInline', () => {
	it('accepts external scripts and stylesheets', () => {
		expect(() => {
			assertNoInline(page('<script src="/a.js"></script><link rel="stylesheet" href="/a.css">'));
		}).not.toThrow();
	});

	it.each([
		['an inline script', '<script>a()</script>', /inline script/],
		['an inline module script', '<script type="module">a()</script>', /inline script/],
		['an inline script with an odd end tag', '<script>a()</script\t\n bar>', /inline script/],
		['a style element', '<style>p{}</style>', /<style>/],
		['a style attribute', '<div style="display: contents"></div>', /style attribute/],
		['an event handler', '<img src="/a.png" onerror="a()">', /event handler/]
	])('rejects %s', (_, body, want) => {
		expect(() => {
			assertNoInline(page(body));
		}).toThrow(want);
	});
});
