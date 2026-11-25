// The UI's CSP (`style-src 'self'`, internal/web/ui) blocks style attributes, and the browser
// logs a violation for each one. This Vite plugin keeps the built app free of them
// (docs/part4-ui.md, "Build"):
//
// - SvelteKit's route announcer (its root.svelte) hides itself with a style attribute. The
//   plugin removes that attribute before Svelte compiles the component; app.css hides
//   #svelte-announcer instead. It fails if the attribute is no longer where it expects.
// - After the client build it fails if any chunk still holds a compiled template with a style
//   attribute, ours or a library's, so a Kit update or a new component can't bring one back.

/** Kit's root component, as Vite names the module (any query stripped). */
const KIT_ROOT = '/@sveltejs/kit/src/runtime/components/root.svelte';

/** The announcer's style attribute in root.svelte (its own line in the opening tag). */
const ANNOUNCER_STYLE = /(<div\s+id="svelte-announcer"[^>]*?)\s+style="[^"]*"/g;

/** A static style attribute inside an HTML tag in a JS string (Svelte compiles templates so).
 * The quote may be escaped (`\'`, `\"`) when the template sits in a quoted string. */
const TEMPLATE_STYLE = /<[a-zA-Z][^<>`]*\sstyle\s*=\s*\\?["'][^<>]*?>/g;

/**
 * Removes the style attribute from Kit's announcer element.
 * @param {string} code root.svelte's source
 * @returns {string}
 */
export function stripAnnouncerStyle(code) {
	const matches = [...code.matchAll(ANNOUNCER_STYLE)];
	if (matches.length !== 1) {
		throw new Error(
			`no-inline-styles: expected one styled #svelte-announcer in Kit's root.svelte, found ${String(matches.length)}`
		);
	}
	return code.replace(ANNOUNCER_STYLE, '$1');
}

/**
 * Returns the template tags in code that carry a style attribute.
 * @param {string} code a built chunk
 * @returns {string[]}
 */
export function findTemplateStyles(code) {
	return [...code.matchAll(TEMPLATE_STYLE)].map((m) => m[0]);
}

/** @returns {import('vite').Plugin} */
export function noInlineStyles() {
	return {
		name: 'ao3mirror:no-inline-styles',
		enforce: 'pre',
		transform(code, id) {
			if (!id.split('?')[0]?.endsWith(KIT_ROOT)) return null;
			return { code: stripAnnouncerStyle(code), map: null };
		},
		generateBundle(_options, bundle) {
			if (this.environment.name !== 'client') return;
			for (const chunk of Object.values(bundle)) {
				if (chunk.type !== 'chunk') continue;
				const found = findTemplateStyles(chunk.code);
				if (found.length > 0) {
					this.error(
						`no-inline-styles: ${chunk.fileName} has a style attribute the CSP blocks: ${found.join(', ').slice(0, 200)}`
					);
				}
			}
		}
	};
}
