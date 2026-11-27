// Runs in headless Chromium (vite.config.ts, the browser project): the sanitizer's safety
// depends on the real HTML parser, and its result on real Trusted Types.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import type { WorkImage } from '#lib/api/api.gen.ts';
import { REVEAL_PARAM, REVEAL_VALUE } from '#lib/reveal.gen.ts';

import { allowedElements, elementAttributes, globalAttributes } from './allowlist.ts';
import { imageMap, missingImageClass, sanitizeHtml, type ImageMap } from './html.ts';

const shaA = 'a'.repeat(64);
const shaB = 'b'.repeat(64);
const imageA = `/api/v1/images/${shaA}?${REVEAL_PARAM}=${REVEAL_VALUE}`;
const imageB = `/api/v1/images/${shaB}?${REVEAL_PARAM}=${REVEAL_VALUE}`;

const images: WorkImage[] = [
	{ src_url: 'https://img.example/fixture-1.png', sha256: shaA, status: 'ok' },
	{ src_url: 'https://Img.Example/a%20b.png', sha256: shaB, status: 'ok' },
	{ src_url: 'https://img.example/queued.png', status: 'queued' },
	{ src_url: 'https://img.example/failed.png', status: 'failed' },
	{ src_url: 'https://img.example/bad-sha.png', sha256: 'not-hex', status: 'ok' }
];

const none: ImageMap = new Map();

/** The markup a TrustedHTML holds (its toString). This page has no CSP, so sinks take it. */
function markup(html: TrustedHTML): string {
	return (html as unknown as { toString(): string }).toString();
}

function clean(dirty: string, map: ImageMap = none): string {
	return markup(sanitizeHtml(dirty, map));
}

/** Markup parsed (inert: DOMParser documents run nothing). */
function parseMarkup(html: string): HTMLElement {
	return new DOMParser().parseFromString(html, 'text/html').body;
}

/** The sanitized result, parsed back. */
function parse(dirty: string, map: ImageMap = none): HTMLElement {
	return parseMarkup(clean(dirty, map));
}

describe('sanitizeHtml', () => {
	it('returns a TrustedHTML', () => {
		expect(window.trustedTypes?.isHTML(sanitizeHtml('<p>Lorem</p>', none))).toBe(true);
	});

	it('keeps otwarchive markup', () => {
		const html =
			'<p align="center" dir="rtl" lang="la" title="t">Lorem <em>ipsum</em> <strong>dolor</strong></p>' +
			'<blockquote cite="https://example.com/q"><p>sit</p></blockquote>' +
			'<ol start="3" type="a"><li>amet</li></ol><ul type="disc"><li>x</li></ul>' +
			'<table border="1" summary="s" width="50%"><thead><tr><th scope="col" abbr="a">h</th></tr></thead>' +
			'<tbody><tr><td colspan="2" rowspan="1">d</td></tr></tbody></table>' +
			'<details open="open"><summary>more</summary>hidden</details>' +
			'<ruby>漢<rp>(</rp><rt>kan</rt><rp>)</rp></ruby><hr align="left" width="50%"><br>' +
			'<p><a name="fn1">1</a> <sup><a href="#fn1">1</a></sup> <s>a</s><del>b</del><ins>c</ins><tt>d</tt></p>';
		expect(clean(html)).toBe(html);
	});

	it('unwraps other elements and keeps their text', () => {
		expect(clean('<p><font color="red">Lorem</font> <marquee>ipsum</marquee> <blink>dolor</blink></p>')).toBe(
			'<p>Lorem ipsum dolor</p>'
		);
	});

	it('removes script, style, svg, math, iframe and the like with their content', () => {
		for (const tag of ['script', 'style', 'svg', 'math', 'iframe', 'noscript', 'noembed', 'noframes', 'xmp']) {
			expect(clean(`<p>a</p><${tag}>gone</${tag}><p>b</p>`), tag).toBe('<p>a</p><p>b</p>');
		}
	});

	it('keeps an attribute only on the elements otwarchive allows it on', () => {
		expect(clean('<p width="10" href="https://example.com" name="x" src="x">a</p>')).toBe('<p>a</p>');
		expect(clean('<span cite="https://example.com" open="">a</span>')).toBe('<span>a</span>');
		expect(clean('<td scope="row">a</td>')).not.toContain('scope'); // a stray td is unwrapped too
		expect(clean('<table><tbody><tr><td scope="row">a</td></tr></tbody></table>')).not.toContain('scope');
	});

	it('drops ids, styles, event handlers, data- and aria- attributes', () => {
		expect(
			clean('<p id="x" style="color:red" onclick="xss()" data-x="1" aria-label="l" role="note" tabindex="1">a</p>')
		).toBe('<p>a</p>');
	});

	it('keeps class names otwarchive allows and drops the rest', () => {
		expect(clean('<p class="lorem  ipsum-2 dolor_x">a</p>')).toBe('<p class="lorem ipsum-2 dolor_x">a</p>');
		expect(clean('<p class="1bad -bad a b@d ok">a</p>')).toBe('<p class="ok">a</p>');
		expect(clean('<p class="1 @">a</p>')).toBe('<p>a</p>');
	});

	it('gives details a plain open', () => {
		expect(clean('<details open="no"><summary>s</summary></details>')).toBe(
			'<details open="open"><summary>s</summary></details>'
		);
	});

	describe('links', () => {
		const href = (raw: string) => parse(`<a href="${raw}">a</a>`).querySelector('a')?.getAttribute('href');

		it('keeps http, https, ftp and mailto', () => {
			expect(href('https://example.com/a?b=c#d')).toBe('https://example.com/a?b=c#d');
			expect(href('http://example.com')).toBe('http://example.com/');
			expect(href('ftp://example.com/f')).toBe('ftp://example.com/f');
			expect(href('mailto:fixture@example.com')).toBe('mailto:fixture@example.com');
		});

		it('keeps fragment links as they are', () => {
			expect(href('#fn1')).toBe('#fn1');
		});

		it('resolves relative links against AO3', () => {
			expect(href('/works/1')).toBe('https://archiveofourown.org/works/1');
			expect(href('works/1?view_full_work=true')).toBe('https://archiveofourown.org/works/1?view_full_work=true');
			expect(href('//example.com/x')).toBe('https://example.com/x');
		});

		it('drops other schemes, however they are spelled', () => {
			for (const raw of [
				'javascript:xss()',
				'JaVaScRiPt:xss()',
				' javascript:xss()',
				'java\tscript:xss()',
				'java&#x09;script:xss()',
				'java&#10;script:xss()',
				'&#106;&#97;&#118;&#97;&#115;&#99;&#114;&#105;&#112;&#116;&#58;xss()',
				'javascript&colon;xss()',
				'vbscript:xss()',
				'data:text/html,<script>xss()</script>',
				'tel:123',
				'blob:https://example.com/x',
				'file:///etc/passwd'
			]) {
				expect(href(raw), raw).toBeNull();
			}
		});

		it('checks cite the same way, without mailto or fragments', () => {
			const cite = (raw: string) => parse(`<q cite="${raw}">a</q>`).querySelector('q')?.getAttribute('cite');
			expect(cite('https://example.com/c')).toBe('https://example.com/c');
			expect(cite('/works/2')).toBe('https://archiveofourown.org/works/2');
			expect(cite('mailto:fixture@example.com')).toBeNull();
			expect(cite('#x')).toBeNull();
			expect(cite('javascript:xss()')).toBeNull();
		});
	});

	describe('images', () => {
		const map = imageMap(images);

		it('points a downloaded image at our image route, with its other attributes', () => {
			expect(clean('<img src="https://img.example/fixture-1.png" alt="Fixture" width="10" height="20" border="0" align="left">', map)).toBe(
				`<img src="${imageA.replaceAll('&', '&amp;')}" alt="Fixture" width="10" height="20" border="0" align="left">`
			);
		});

		it('matches the stored URL the way the browser parses both', () => {
			for (const src of [
				'  https://img.example/fixture-1.png  ',
				'https://img.example/fixture-1.png#frag',
				'HTTPS://IMG.EXAMPLE/fixture-1.png',
				'https://img.example:443/fixture-1.png'
			]) {
				expect(parse(`<img src="${src}">`, map).querySelector('img')?.getAttribute('src'), src).toBe(imageA);
			}
			expect(parse('<img src="https://img.example/a b.png">', map).querySelector('img')?.getAttribute('src')).toBe(imageB);
		});

		it('leaves a note where an image is not downloaded', () => {
			for (const src of [
				'https://img.example/queued.png',
				'https://img.example/failed.png',
				'https://img.example/bad-sha.png',
				'https://img.example/unknown.png',
				'/images/relative.png',
				'data:image/png;base64,AAAA',
				imageA,
				''
			]) {
				const body = parse(`<p><img src="${src}" alt=" Fixture alt "></p>`, map);
				expect(body.querySelector('img'), src).toBeNull();
				const note = body.querySelector(`span.${missingImageClass}`);
				expect(note?.textContent, src).toBe('[Image not mirrored: Fixture alt]');
			}
			expect(clean('<img>', map)).toBe(`<span class="${missingImageClass}">[Image not mirrored]</span>`);
		});

		it('never keeps a src it did not write', () => {
			expect(clean('<img src="https://img.example/fixture-1.png" srcset="https://evil.example/x.png 2x">', map)).not.toContain(
				'evil'
			);
			expect(parse('<img src="https://img.example/fixture-1.png"><img src="x">', map).querySelectorAll('img')).toHaveLength(1);
		});

		it('builds the map from downloaded images only', () => {
			expect([...map.values()]).toEqual([shaA, shaB]);
		});
	});
});

// Known XSS, mXSS and DOM-clobbering payloads (OWASP's filter-evasion cheat sheet, PortSwigger's
// XSS cheat sheet and DOMPurify's published bypasses). Each calls xss() if it ever runs.
const payloads = [
	'<script>xss()</script>',
	'<script src="data:,xss()"></script>',
	'<img src=x onerror=xss()>',
	'<img src="x" onerror="xss()" />',
	'<img/src=x/onerror=xss()>',
	'<IMG SRC=x OnErRoR=xss()>',
	'<img src=`x`onerror=xss()>',
	'<img """><script>xss()</script>">',
	'<svg onload=xss()>',
	'<svg><script>xss()</script></svg>',
	'<svg><animate onbegin=xss() attributeName=x dur=1s>',
	'<svg><a xlink:href="javascript:xss()"><text x="20" y="20">x</text></a></svg>',
	'<math><mtext><table><mglyph><style><img src=x onerror=xss()>',
	'<math><mi><mglyph><svg><mtext><textarea><path id="</textarea><img onerror=xss() src=1>">',
	'<svg></p><style><a id="</style><img src=1 onerror=xss()>">',
	'<svg><p><style><img src=x onerror=xss()></style></p></svg>',
	'<noscript><p title="</noscript><img src=x onerror=xss()>">',
	'<form><math><mtext></form><form><mglyph><style></math><img src onerror=xss()>',
	'<a href="javascript:xss()">x</a>',
	'<a href="&#x6A;avascript:xss()">x</a>',
	'<a href="jav&#x09;ascript:xss()">x</a>',
	'<a href=" &#14; javascript:xss()">x</a>',
	'<a href="data:text/html;base64,PHNjcmlwdD54c3MoKTwvc2NyaXB0Pg==">x</a>',
	'<a href="#" onclick="xss()">x</a>',
	'<a href="https://example.com" target="_blank" ping="https://evil.example">x</a>',
	'<iframe src="javascript:xss()"></iframe>',
	'<iframe srcdoc="<script>parent.xss()</script>"></iframe>',
	'<object data="javascript:xss()"></object>',
	'<embed src="javascript:xss()">',
	'<base href="javascript:xss()//">',
	'<meta http-equiv="refresh" content="0;url=javascript:xss()">',
	'<link rel="stylesheet" href="https://evil.example/x.css">',
	'<style>@import "https://evil.example/x.css";</style>',
	'<div style="background:url(javascript:xss())">x</div>',
	'<form action="javascript:xss()"><button>x</button></form>',
	'<button formaction="javascript:xss()">x</button>',
	'<input autofocus onfocus=xss()>',
	'<select autofocus onfocus=xss()></select>',
	'<textarea autofocus onfocus=xss()></textarea>',
	'<details open ontoggle=xss()>',
	'<video><source onerror=xss()></video>',
	'<audio src=x onerror=xss()>',
	'<body onload=xss()>',
	'<marquee onstart=xss()>x</marquee>',
	'<template><img src=x onerror=xss()></template>',
	'<!--<img src=x onerror=xss()>-->',
	'<!--><img src=x onerror=xss()>-->',
	'<![CDATA[><img src=x onerror=xss()>]]>',
	'<title><img src=x onerror=xss()></title>',
	'<xmp><img src=x onerror=xss()></xmp>',
	'<plaintext><img src=x onerror=xss()>',
	'<p title="</p><img src=x onerror=xss()>">x</p>',
	'<p id="cookie">x</p><p name="body">y</p>',
	'<a name="getElementById">x</a><a name="attributes">y</a>',
	'<form id="test"><input name="nodeName"></form>',
	'<img name="createElement" src="x">',
	'<x/onerror=xss()>',
	'<div is="x-evil" onclick="xss()">x</div>',
	'<custom-element onclick="xss()">x</custom-element>',
	'"><img src=x onerror=xss()>',
	'</p><img src=x onerror=xss()>',
	'<scr<script>ipt>xss()</scr</script>ipt>',
	'<<script>xss()//<</script>',
	'<a href="https://example.com/\u2028javascript:xss()">x</a>',
	'<img src="https://img.example/fixture-1.png" onload="xss()" onerror="xss()">'
];

describe('payloads', () => {
	let fired: string[] = [];

	beforeEach(() => {
		fired = [];
		Object.assign(window, { xss: () => fired.push('fired') });
	});

	afterEach(() => {
		document.body.replaceChildren();
	});

	const tags = new Set([...allowedElements, 'span']);
	const attrs = new Set(globalAttributes);
	const urlCheck: Record<string, RegExp> = {
		href: /^(?:(?:https?|ftp):\/\/|mailto:|#)/,
		cite: /^https?:\/\//,
		src: new RegExp(`^/api/v1/images/[0-9a-f]{64}\\?${REVEAL_PARAM}=${REVEAL_VALUE}$`)
	};

	it.each(payloads)('neutralizes %s', async (payload) => {
		const out = clean(payload, imageMap(images));
		const body = parseMarkup(out);

		for (const el of body.querySelectorAll('*')) {
			const tag = el.localName;
			expect(tags.has(tag), `element ${tag}`).toBe(true);
			expect(el.namespaceURI).toBe('http://www.w3.org/1999/xhtml');
			for (const { name, value } of el.attributes) {
				if (name === 'is') {
					// DOMPurify empties `is` rather than removing it: a parsed element keeps
					// the custom element it was created as.
					expect(value).toBe('');
					continue;
				}
				expect(attrs.has(name) || elementAttributes[tag]?.includes(name), `${tag}[${name}]`).toBe(true);
				const check = urlCheck[name];
				if (check !== undefined) {
					expect(value, `${tag}[${name}]`).toMatch(check);
				}
			}
		}

		// Stable: sanitizing the output again changes nothing (a change means the markup parses
		// differently the second time, which is how mutation XSS works). Not with an image: its
		// rewritten src is our own route, which isn't in the image map.
		if (body.querySelector('img') === null) {
			expect(clean(out, imageMap(images))).toBe(out);
		}

		// Live: in the page, nothing runs. Without the image map, so no image is requested.
		document.body.append(document.createRange().createContextualFragment(clean(payload)));
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(fired).toEqual([]);
	});

	it('detects a payload that runs (the check above can fail)', async () => {
		document.body.append(document.createRange().createContextualFragment('<img src="data:," onerror="xss()">'));
		await expect.poll(() => fired).toEqual(['fired']);
	});
});
