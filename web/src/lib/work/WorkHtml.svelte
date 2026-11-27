<!--
	One fragment of a work's HTML (a summary, notes, later a chapter), through the HTML sanitizer.
	`images` is the work's imageMap; a listing blurb has none, so its images are dropped.
-->
<script lang="ts">
	import TrustedHtml from '#lib/html/TrustedHtml.svelte';
	import { type ImageMap, sanitizeHtml } from '#lib/sanitize/html.ts';

	let { html, images = new Map() }: { html: string; images?: ImageMap } = $props();

	const clean = $derived(sanitizeHtml(html, images));
</script>

<div class="userstuff"><TrustedHtml html={clean} /></div>

<style>
	.userstuff {
		font-family: var(--font-reading);
		overflow-wrap: break-word;
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
