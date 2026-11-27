<!--
	One fragment of a work's HTML (a summary, notes, a chapter's text), through the HTML
	sanitizer. `images` is the work's imageMap; a listing blurb has none, so its images are
	dropped. The wrapper is AO3's, since work skins select it: `blockquote.userstuff` for a
	summary or notes, `div.userstuff.module[role=article]` for a chapter's text, and a plain
	`div.userstuff` for a one-shot's.
-->
<script lang="ts">
	import TrustedHtml from '#lib/html/TrustedHtml.svelte';
	import { type ImageMap, sanitizeHtml } from '#lib/sanitize/html.ts';

	let {
		html,
		images = new Map(),
		as = 'div',
		chapterText = false,
		label
	}: {
		html: string;
		images?: ImageMap;
		as?: 'div' | 'blockquote';
		/** A chaptered work's chapter text: AO3's `module` class and `article` role. */
		chapterText?: boolean;
		/** An accessible name, which makes the wrapper a region (a one-shot's text). */
		label?: string;
	} = $props();

	const clean = $derived(sanitizeHtml(html, images));
	const role = $derived(chapterText ? 'article' : label === undefined ? undefined : 'region');
</script>

<svelte:element this={as} class={['userstuff', chapterText && 'module']} {role} aria-label={label}
	><TrustedHtml html={clean} /></svelte:element
>

<style>
	.userstuff {
		font-family: var(--font-reading);
		overflow-wrap: break-word;
	}

	blockquote.userstuff {
		margin: 0;
	}

	.module {
		margin-block: var(--space-3);
	}

	.userstuff :global(img) {
		max-width: 100%;
		height: auto;
	}

	.userstuff :global(blockquote) {
		margin-inline: var(--space-3) 0;
		padding-inline-start: var(--space-2);
		border-inline-start: 2px solid var(--color-muted);
	}

	/* html.ts's missingImageClass: the note left where an image that isn't mirrored was. */
	.userstuff :global(.ao3m-missing-image) {
		color: var(--color-muted);
		font-style: italic;
	}
</style>
