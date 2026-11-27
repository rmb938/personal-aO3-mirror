// See https://svelte.dev/docs/kit/types#app.d.ts for the App namespace.
declare global {
	namespace App {
		interface PageState {
			/** Set on the chapter a refused entire-work view fell back to (work-too-large). */
			workTooLarge?: boolean;
		}
	}
}

export {};
