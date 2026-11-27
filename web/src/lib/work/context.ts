// The work a work page's layout has loaded, handed to its views (the entire work, one
// chapter), so moving between them doesn't fetch the work again.

import { createContext } from 'svelte';

import type { Work } from '#lib/api/api.gen.ts';
import type { ImageMap } from '#lib/sanitize/html.ts';

/** The work's skin, as the layout loaded and sanitized it (docs/part4-ui.md stage 8c). */
export type Skin =
	/** No skin, or none of it survived the sanitizer. */
	| { kind: 'none' }
	/** The sanitized sheet; the layout adopts it while `shown`. */
	| { kind: 'sheet'; sheet: CSSStyleSheet }
	/** The skin couldn't be loaded: the work is shown without it. */
	| { kind: 'failed' };

/** The loaded work and its imageMap. The layout renders its views only once the work is in. */
export interface WorkContext {
	readonly work: Work;
	readonly images: ImageMap;
	readonly skin: Skin;
	/** Whether the skin applies; "Hide Creator's Style" clears it, for this work. */
	skinShown: boolean;
}

export const [getWorkContext, setWorkContext] = createContext<WorkContext>();
