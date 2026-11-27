// otwarchive's HTML allowlist, the one AO3 sanitizes work text with (docs/part4-ui.md,
// "Security model"). Read from otwcode/otwarchive at commit
// 452391b648f2fb06b55c36133812449747634504 (2026-11-01):
// config/initializers/gem-plugin_config/sanitizer_config.rb (Sanitize::Config::ARCHIVE and
// CSS_ALLOWED, which adds `class`) and lib/otw_sanitize/user_class_sanitizer.rb (which class
// names survive). Re-read those files when bumping the commit.
//
// Left out on purpose: otwarchive's media and embed sanitizers (audio, video, iframe embeds).
// They load from other hosts, which the UI's CSP refuses and docs/part4-ui.md rules out
// ("No iframes"), so those elements are dropped.

/** Elements kept (ARCHIVE `elements`). Any other element is removed and its content kept. */
export const allowedElements: readonly string[] = [
	'a', 'abbr', 'acronym', 'address', 'b', 'big', 'blockquote', 'br', 'caption', 'center', 'cite',
	'code', 'col', 'colgroup', 'details', 'figcaption', 'figure', 'dd', 'del', 'dfn', 'div', 'dl',
	'dt', 'em', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'i', 'img', 'ins', 'kbd', 'li', 'ol', 'p',
	'pre', 'q', 'rp', 'rt', 'ruby', 's', 'samp', 'small', 'span', 'strike', 'strong', 'sub',
	'summary', 'sup', 'table', 'tbody', 'td', 'tfoot', 'th', 'thead', 'tr', 'tt', 'u', 'ul', 'var'
];

/** Attributes any kept element may have (ARCHIVE `attributes.all`, plus CSS_ALLOWED's class). */
export const globalAttributes: readonly string[] = ['align', 'dir', 'lang', 'title', 'class'];

/** Attributes only these elements may have (ARCHIVE `attributes`). */
export const elementAttributes: Readonly<Record<string, readonly string[]>> = {
	a: ['href', 'name'],
	blockquote: ['cite'],
	col: ['span', 'width'],
	colgroup: ['span', 'width'],
	details: ['open'],
	hr: ['align', 'width'],
	img: ['align', 'alt', 'border', 'height', 'src', 'width'],
	ol: ['start', 'type'],
	q: ['cite'],
	table: ['border', 'summary', 'width'],
	td: ['abbr', 'axis', 'colspan', 'height', 'rowspan', 'width'],
	th: ['abbr', 'axis', 'colspan', 'height', 'rowspan', 'scope', 'width'],
	ul: ['type']
};

/**
 * URL schemes per element and attribute (ARCHIVE `protocols`); `relative` allows a URL without
 * a scheme. `img src` (http and https in ARCHIVE) is not here: the sanitizer replaces every
 * image's src with our own image route, or drops the image.
 */
export const urlSchemes: Readonly<Record<string, Readonly<Record<string, readonly string[]>>>> = {
	a: { href: ['ftp:', 'http:', 'https:', 'mailto:', 'relative'] },
	blockquote: { cite: ['http:', 'https:', 'relative'] },
	q: { cite: ['http:', 'https:', 'relative'] }
};

/**
 * Elements removed together with their content (ARCHIVE `remove_contents`). DOMPurify's own
 * list (FORBID_CONTENTS) already has every one of them; the sanitizer adds them anyway, so
 * this stays true if DOMPurify's list changes.
 */
export const removedWithContent: readonly string[] = [
	'iframe', 'math', 'noembed', 'noframes', 'noscript', 'plaintext', 'script', 'style', 'svg', 'xmp'
];

/** A class name AO3 keeps (UserClassSanitizer#valid_class?): a letter, then 1+ word chars or dashes. */
export const validClass = /^[a-zA-Z][\w-]+$/;

/**
 * AO3's address. A relative link in work text points into AO3, where it was written, so the
 * sanitizer resolves it against this (as otwarchive does for images, with APP_URL).
 */
export const ao3Origin = 'https://archiveofourown.org/';
