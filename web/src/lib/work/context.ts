// The work a work page's layout has loaded, handed to its views (the entire work, one
// chapter), so moving between them doesn't fetch the work again.

import { createContext } from 'svelte';

import type { Work } from '#lib/api/api.gen.ts';
import type { ImageMap } from '#lib/sanitize/html.ts';

/** The loaded work and its imageMap. The layout renders its views only once the work is in. */
export interface WorkContext {
	readonly work: Work;
	readonly images: ImageMap;
}

export const [getWorkContext, setWorkContext] = createContext<WorkContext>();
