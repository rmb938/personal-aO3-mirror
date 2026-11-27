<!--
	One chapter, as otwarchive's chapters/_chapter: for a chaptered work its heading (linking to
	the chapter's own page), summary and notes, then the text and the end notes. A one-shot shows
	the text and end notes alone. The ids and classes are AO3's, so a work skin (stage 8c) finds
	what it was written for.
-->
<script lang="ts">
	import { resolve } from '$app/paths';

	import type { Chapter } from '#lib/api/api.gen.ts';
	import type { ImageMap } from '#lib/sanitize/html.ts';

	import WorkHtml from './WorkHtml.svelte';

	let {
		chapter,
		workId,
		chaptered,
		images
	}: { chapter: Chapter; workId: number; chaptered: boolean; images: ImageMap } = $props();

	const p = $derived(String(chapter.position));
	const title = $derived(chapter.title?.trim() ?? '');
	const endNotesId = $derived(`chapter_${p}_endnotes`);
</script>

<section
	class="chapter"
	id="chapter-{p}"
	aria-labelledby={chaptered ? `chapter-${p}-heading` : undefined}
	aria-label={chaptered ? undefined : 'Work text'}
>
	{#if chaptered}
		<div class="chapter preface group">
			<h2 class="title" id="chapter-{p}-heading">
				<a href={resolve('/(app)/works/[id=id]/chapters/[position=position]', { id: workId, position: chapter.position })}
					>Chapter {p}</a
				>{#if title !== ''}: {title}{/if}
			</h2>
			{#if chapter.summary_html !== undefined}
				<section class="summary module" aria-labelledby="chapter-{p}-summary">
					<h3 class="heading" id="chapter-{p}-summary">Chapter Summary</h3>
					<WorkHtml html={chapter.summary_html} {images} />
				</section>
			{/if}
			{#if chapter.notes_html !== undefined || chapter.end_notes_html !== undefined}
				<section class="notes module" aria-labelledby="chapter-{p}-notes">
					<h3 class="heading" id="chapter-{p}-notes">Chapter Notes</h3>
					{#if chapter.notes_html !== undefined}
						<WorkHtml html={chapter.notes_html} {images} />
					{/if}
					{#if chapter.end_notes_html !== undefined}
						<p class="jump">
							(See the end of the chapter for <a href="#{endNotesId}"
								>{chapter.notes_html === undefined ? 'notes' : 'more notes'}</a
							>.)
						</p>
					{/if}
				</section>
			{/if}
		</div>
	{/if}

	<div class="text module">
		<WorkHtml html={chapter.body_html} {images} />
	</div>

	{#if chapter.end_notes_html !== undefined}
		<div class="chapter preface group">
			<section class="end notes module" id={endNotesId} aria-labelledby="{endNotesId}-heading">
				<h3 class="heading" id="{endNotesId}-heading">Chapter End Notes</h3>
				<WorkHtml html={chapter.end_notes_html} {images} />
			</section>
		</div>
	{/if}
</section>

<style>
	/*
		AO3's preface group (14-group-preface.css): a rule above each of a chapter's groups, so
		one marks where a chapter starts (above its heading) and another where its text ends
		(above its end notes), and the groups set in from the text on a wide screen.
	*/
	.preface {
		margin-block: var(--space-4) var(--space-3);
		padding-top: var(--space-2);
		border-top: 1px solid var(--color-muted);
	}

	@media (min-width: 40rem) {
		.preface {
			margin-inline: var(--space-4);
		}
	}

	h2 {
		font-size: 1.25rem;
		text-align: center;
	}

	/* AO3's ".preface h3": a rule under each "Notes" or "Summary" heading. */
	h3 {
		padding-bottom: var(--space-1);
		border-bottom: 1px solid var(--color-muted);
		font-size: 1rem;
	}

	.text {
		margin-block: var(--space-3);
	}

	.jump {
		color: var(--color-muted);
		font-size: 0.875rem;
	}
</style>
