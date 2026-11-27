// The skin sanitizer: the only way a work skin becomes CSS on the page (docs/part4-ui.md,
// "Security model"). The browser's own CSS parser reads the skin, so nothing here can disagree
// with how the browser would read it. Of what it parsed, only plain style rules are kept, each
// with the declarations otwarchive's allowlist (./skin-allowlist.ts) and work-skin rules allow,
// and with no function that could load anything. They are copied, one rule and one property
// at a time through the CSSOM (never as text), into a new sheet inside
// `@scope (.ao3m-skin-scope)`, so they can only match elements inside that element. The page
// applies the result with `adoptedStyleSheets`.

import { cssProperties, cssShorthands, vendorPrefixes } from './skin-allowlist.ts';

/**
 * The class of the element the skin is scoped to. The work page puts `#workskin` (what AO3's
 * selectors start with) directly inside it, and it inside a frame the skin can't reach that
 * keeps the skin's painting inside it (docs/part4-ui.md, stage 8).
 */
export const skinScopeClass = 'ao3m-skin-scope';

/**
 * Sanitizes a work skin into a stylesheet for `adoptedStyleSheets`, or undefined when nothing
 * in it survives (or the browser has no `@scope`, which the result depends on).
 */
export function sanitizeSkin(css: string): CSSStyleSheet | undefined {
	if (!('CSSScopeRule' in window)) {
		return undefined;
	}
	// A constructed sheet never loads @import; the loop below drops it with every other at-rule.
	const input = new CSSStyleSheet();
	input.replaceSync(renameImports(css));

	const out = new CSSStyleSheet();
	out.insertRule(`@scope (.${skinScopeClass}) {}`);
	const scope = out.cssRules[0];
	if (!(scope instanceof CSSScopeRule)) {
		return undefined;
	}
	for (const rule of input.cssRules) {
		// At-rules (@import, @font-face, @media, @keyframes, @layer, ...) are dropped, as AO3's
		// cleaner keeps only rule sets. A style rule's nested rules are dropped too.
		if (!(rule instanceof CSSStyleRule)) {
			continue;
		}
		const declarations = allowedDeclarations(rule.style);
		if (declarations.length === 0) {
			continue;
		}
		let index: number;
		try {
			// The selector is the browser's own serialization of one it parsed; insertRule
			// takes exactly one rule, so it can't add anything else.
			index = scope.insertRule(`${rule.selectorText} {}`, scope.cssRules.length);
		} catch {
			continue;
		}
		const copy = scope.cssRules[index];
		if (!(copy instanceof CSSStyleRule) || copy.selectorText !== rule.selectorText) {
			scope.deleteRule(index);
			continue;
		}
		for (const d of declarations) {
			copy.style.setProperty(d.name, d.value, d.priority);
		}
	}
	return scope.cssRules.length > 0 ? out : undefined;
}

/** What renameImports turns `@import` into: an at-rule no browser knows. */
const renamedImport = '@ao3m-dropped-import';

/**
 * Renames every `@import` so the browser reads it as an unknown at-rule. Chrome logs a warning
 * for each @import a constructed sheet refuses, and an unknown at-rule is dropped silently. It
 * changes no rule's extent (both are at-rules, ending at the same `;` or block), only the name,
 * and protects nothing: an @import it misses (written with an escape) is still refused by
 * replaceSync and dropped by the loop, at the cost of a warning. Inside a string or comment it
 * changes the text, which a skin has no reason to hold.
 */
function renameImports(css: string): string {
	return css.replace(/@import/gi, renamedImport);
}

interface Declaration {
	name: string;
	value: string;
	priority: string;
}

/** The declarations of a parsed rule that may stay, as longhands (how the browser holds them). */
function allowedDeclarations(style: CSSStyleDeclaration): Declaration[] {
	const out: Declaration[] = [];
	for (const name of style) {
		// A longhand of a shorthand that used var() has no value of its own (it waits for the
		// variable): it is dropped like var() itself.
		const value = style.getPropertyValue(name).trim();
		if (value === '' || !allowedProperty(name) || !allowedValue(name, value)) {
			continue;
		}
		out.push({ name, value, priority: style.getPropertyPriority(name) });
	}
	return out;
}

const propertySet = new Set(cssProperties);

const vendorProperty = new RegExp(`^-(?:${vendorPrefixes.join('|')})-(.+)$`);

/** legal_property? or legal_shorthand_property? (a shorthand name anywhere in the name). */
function allowedProperty(name: string): boolean {
	if (name.startsWith('--')) {
		return false; // work skins may not define custom properties
	}
	const unprefixed = vendorProperty.exec(name)?.[1];
	if (propertySet.has(name) || (unprefixed !== undefined && propertySet.has(unprefixed))) {
		return true;
	}
	return cssShorthands.some((s) => name.includes(s));
}

/** A function call in a serialized value: its name, then `(`. */
const functionCall = /([-a-z0-9_]+)\(/gi;

function allowedValue(name: string, value: string): boolean {
	if (name === 'position' && value.toLowerCase() === 'fixed') {
		return false; // WorkSkin#clean_css
	}
	// Every function must be on the list. That rules out url(), image-set(), cross-fade(),
	// element(), paint(), src() and the like (anything that loads), var() and env() (work skins
	// may not use variables), and attr(). Strings are matched too, which only drops more.
	for (const m of value.matchAll(functionCall)) {
		if (!allowedFunction((m[1] ?? '').toLowerCase())) {
			return false;
		}
	}
	return true;
}

/** CSS functions that compute a value from their arguments and load nothing. */
const functions = new Set([
	// colors
	'rgb', 'rgba', 'hsl', 'hsla', 'hwb', 'lab', 'lch', 'oklab', 'oklch', 'color', 'color-mix',
	'light-dark', 'contrast-color',
	// math
	'calc', 'min', 'max', 'clamp', 'round', 'mod', 'rem', 'abs', 'sign',
	// gradients (the old -webkit-gradient takes color-stop, from and to)
	'-webkit-gradient', 'color-stop', 'from', 'to',
	// transforms
	'matrix', 'matrix3d', 'translate', 'translatex', 'translatey', 'translatez', 'translate3d',
	'scale', 'scalex', 'scaley', 'scalez', 'scale3d', 'rotate', 'rotatex', 'rotatey', 'rotatez',
	'rotate3d', 'skew', 'skewx', 'skewy', 'perspective',
	// filters
	'blur', 'brightness', 'contrast', 'drop-shadow', 'grayscale', 'hue-rotate', 'invert',
	'opacity', 'saturate', 'sepia',
	// shapes and timing
	'rect', 'inset', 'circle', 'ellipse', 'polygon', 'cubic-bezier', 'steps', 'linear',
	// generated content and layout
	'counter', 'counters', 'repeat', 'minmax', 'fit-content'
]);

const gradient = /^(?:-(?:webkit|moz|o|ms)-)?(?:repeating-)?(?:linear|radial|conic)-gradient$/;

function allowedFunction(name: string): boolean {
	return functions.has(name) || gradient.test(name);
}
