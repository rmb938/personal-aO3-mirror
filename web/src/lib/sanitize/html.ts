// The HTML sanitizer: the only way work text (chapters, notes, summaries) becomes markup on the
// page (docs/part4-ui.md, "Security model"). DOMPurify, limited to otwarchive's allowlist
// (./allowlist.ts), returns a TrustedHTML from our Trusted Types policy, which
// TrustedHtml.svelte renders. Images are rewritten to our own image route or dropped.

import DOMPurify, {
	type Config,
	type DOMPurify as Purifier,
	type UponSanitizeAttributeHookEvent,
	type UponSanitizeElementHookEvent
} from 'dompurify';

import { getGetImageUrl, type WorkImage } from '#lib/api/api.gen.ts';
import { withReveal } from '#lib/api/http.ts';

import {
	allowedElements,
	ao3Origin,
	elementAttributes,
	globalAttributes,
	removedWithContent,
	urlSchemes,
	validClass
} from './allowlist.ts';

/**
 * The sanitizer's Trusted Types policy. The UI's CSP (internal/web/ui) allows exactly this name
 * and Svelte's, each created once, so nothing else can make a TrustedHTML.
 */
export const policyName = 'ao3mirror-html';

/** The class of the text left where an image that isn't mirrored was. */
export const missingImageClass = 'ao3m-missing-image';

/** A work's downloaded images: the canonical `<img src>` URL to the image's SHA-256. */
export type ImageMap = ReadonlyMap<string, string>;

const sha256Hex = /^[0-9a-f]{64}$/;

/**
 * Builds the image map from a work's `images`. Only downloaded ones (status `ok`) are in it, so
 * every other image is dropped.
 */
export function imageMap(images: readonly WorkImage[]): ImageMap {
	const out = new Map<string, string>();
	for (const img of images) {
		if (img.status !== 'ok' || img.sha256 === undefined || !sha256Hex.test(img.sha256)) {
			continue;
		}
		const key = canonicalImageUrl(img.src_url);
		if (key !== undefined && !out.has(key)) {
			out.set(key, img.sha256);
		}
	}
	return out;
}

/**
 * The key an image URL is matched by. The scraper stores an `<img src>` trimmed, as an absolute
 * http(s) URL without its fragment (imagefetch.normalize, in Go); both the stored URL and the
 * src in the HTML go through the browser's URL parser here, so spelling differences the two
 * parsers treat differently (host case, escaping) don't stop a match.
 */
function canonicalImageUrl(raw: string): string | undefined {
	const u = URL.parse(raw.trim());
	if (u === null || (u.protocol !== 'http:' && u.protocol !== 'https:') || u.host === '') {
		return undefined;
	}
	u.hash = '';
	return u.href;
}

/** Sanitizes one fragment of work HTML; `images` comes from imageMap. */
export function sanitizeHtml(dirty: string, images: ImageMap): TrustedHTML {
	const p = purifier();
	call = { images, imageSrcs: new Set() };
	try {
		if (p.config.TRUSTED_TYPES_POLICY === null) {
			// No Trusted Types in this browser, so no sink asks for a TrustedHTML: the
			// sanitized string is what it takes.
			return p.purify.sanitize(dirty, p.config) as unknown as TrustedHTML;
		}
		return p.purify.sanitize(dirty, { ...p.config, RETURN_TRUSTED_TYPE: true });
	} finally {
		call = undefined;
	}
}

/** What the hooks need from the sanitizeHtml call they run in (sanitize is synchronous). */
interface Call {
	images: ImageMap;
	/** The image URLs this call wrote, the only `img src` values kept. */
	imageSrcs: Set<string>;
}

let call: Call | undefined;

interface Purifying {
	purify: Purifier;
	config: Config;
}

let instance: Purifying | undefined;

/**
 * Our own DOMPurify instance (so its hooks reach no other caller) and its config, made on first
 * use: the policy can be created only once, and only in a browser.
 */
function purifier(): Purifying {
	if (instance !== undefined) {
		return instance;
	}
	const purify = DOMPurify(window);
	purify.addHook('uponSanitizeElement', onElement);
	purify.addHook('uponSanitizeAttribute', onAttribute);
	// The policy passes strings through: DOMPurify calls it on its input (to parse it) and on
	// its finished output. It never leaves this module, so the only TrustedHTML it makes for
	// anyone is sanitizeHtml's result.
	const policy =
		// eslint-disable-next-line no-restricted-properties -- this is the one policy
		window.trustedTypes?.createPolicy(policyName, {
			createHTML: (s) => s,
			createScript: () => {
				throw new TypeError(`${policyName} makes no scripts`);
			},
			createScriptURL: () => {
				throw new TypeError(`${policyName} makes no script URLs`);
			}
		}) ?? null;
	const config: Config = {
		ALLOWED_TAGS: [...allowedElements],
		ALLOWED_ATTR: [...allowedAttributeNames],
		ALLOW_DATA_ATTR: false,
		ALLOW_ARIA_ATTR: false,
		ADD_FORBID_CONTENTS: [...removedWithContent],
		// null when the browser has no Trusted Types: DOMPurify then makes no policy of its own
		// (the CSP would refuse its name).
		TRUSTED_TYPES_POLICY: policy
	};
	instance = { purify, config };
	return instance;
}

const globalAttributeSet = new Set(globalAttributes);

const elementAttributeSets = new Map(
	Object.entries(elementAttributes).map(([tag, names]) => [tag, new Set(names)])
);

/** Every attribute name kept on some element; onAttribute narrows it per element. */
const allowedAttributeNames = new Set([...globalAttributes, ...Object.values(elementAttributes).flat()]);

/**
 * Points an image at our image route when the work's image is downloaded, and otherwise puts a
 * short note in its place (the alt text, if any). Setting src here, before the attributes are
 * checked, means onAttribute and DOMPurify still check it.
 */
function onElement(node: Node, data: UponSanitizeElementHookEvent): void {
	if (data.tagName !== 'img' || node.nodeType !== Node.ELEMENT_NODE || call === undefined) {
		return;
	}
	const img = node as Element;
	const key = canonicalImageUrl(img.getAttribute('src') ?? '');
	const sha = key === undefined ? undefined : call.images.get(key);
	if (sha !== undefined) {
		const src = withReveal(getGetImageUrl(sha));
		call.imageSrcs.add(src);
		img.setAttribute('src', src);
		return;
	}
	const note = img.ownerDocument.createElement('span');
	note.className = missingImageClass;
	const alt = img.getAttribute('alt')?.trim() ?? '';
	note.textContent = alt === '' ? '[Image not mirrored]' : `[Image not mirrored: ${alt}]`;
	img.replaceWith(note);
}

/**
 * Applies the per-element parts of otwarchive's allowlist, which DOMPurify's flat attribute
 * list can't express: which element may have which attribute, URL schemes, class names, and
 * details' open.
 */
function onAttribute(node: Element, data: UponSanitizeAttributeHookEvent): void {
	const tag = node.nodeName.toLowerCase();
	const name = data.attrName;
	if (!globalAttributeSet.has(name) && elementAttributeSets.get(tag)?.has(name) !== true) {
		data.keepAttr = false;
		return;
	}
	if (name === 'class') {
		const kept = data.attrValue.split(/\s+/).filter((c) => validClass.test(c));
		if (kept.length === 0) {
			data.keepAttr = false;
		} else {
			data.attrValue = kept.join(' ');
		}
		return;
	}
	if (tag === 'details' && name === 'open') {
		data.attrValue = 'open'; // otwarchive's OPEN_ATTRIBUTE_TRANSFORMER
		return;
	}
	if (tag === 'img' && name === 'src') {
		if (call?.imageSrcs.has(data.attrValue) !== true) {
			data.keepAttr = false;
		}
		return;
	}
	const schemes = urlSchemes[tag]?.[name];
	if (schemes !== undefined) {
		const url = allowedUrl(data.attrValue, schemes, tag === 'a');
		if (url === undefined) {
			data.keepAttr = false;
		} else {
			data.attrValue = url;
		}
	}
}

/**
 * The URL to keep for a link or citation, or undefined to drop it. A URL with a scheme must use
 * one of `schemes` (as the browser's URL parser reads it, so `java\tscript:` is `javascript:`);
 * one without needs `relative` and is resolved against AO3, where it was written. A link that
 * is only a fragment (`#fn1`, AO3's footnotes) stays as it is.
 */
function allowedUrl(raw: string, schemes: readonly string[], fragmentOK: boolean): string | undefined {
	const value = raw.trim();
	if (value.startsWith('#')) {
		return fragmentOK ? value : undefined;
	}
	const absolute = URL.parse(value);
	if (absolute !== null) {
		return schemes.includes(absolute.protocol) ? absolute.href : undefined;
	}
	if (!schemes.includes('relative')) {
		return undefined;
	}
	const resolved = URL.parse(value, ao3Origin);
	if (resolved === null || (resolved.protocol !== 'https:' && resolved.protocol !== 'http:')) {
		return undefined;
	}
	return resolved.href;
}
