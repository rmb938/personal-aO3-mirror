<!--
	A chaptered work's navigation, as otwarchive's work header navigation. At the top: Entire Work
	(or Chapter by Chapter), the previous and next chapter, and the chapter index. At the bottom:
	the previous and next chapter and back to the top. Previous and next land on #workskin, past
	the metadata, like AO3's; Chapter by Chapter and the index open a chapter at the top.
-->
<script lang="ts">
	import { resolve } from '$app/paths';

	import type { ChapterRef } from '#lib/api/api.gen.ts';

	import { type ChapterPlace, chapterHeading, fullWorkParam, type ReadingView } from './chapters.ts';

	let {
		workId,
		chapters,
		view,
		place,
		at
	}: {
		workId: number;
		chapters: readonly ChapterRef[];
		view: ReadingView;
		/** Where the shown chapter sits; undefined for the entire work (or a chapter not in the list). */
		place: ChapterPlace | undefined;
		at: 'top' | 'bottom';
	} = $props();

	function chapterHref(position: number): string {
		return resolve('/(app)/works/[id=id]/chapters/[position=position]', { id: workId, position });
	}

	/** Previous and next land past the metadata, as AO3's do; its other chapter links don't. */
	function stepHref(position: number): string {
		return `${chapterHref(position)}#workskin`;
	}

	const fullHref = $derived(`${resolve('/(app)/works/[id=id]', { id: workId })}?${fullWorkParam}=true`);
	const first = $derived(chapters[0]?.position ?? 1);
	const current = $derived(view.kind === 'chapter' ? view.position : undefined);
</script>

<!-- The top one is the bottom one's "↑ Top". -->
<nav class="chapter-nav" id={at === 'top' ? 'top' : undefined} aria-label={at === 'top' ? 'Chapters' : 'Chapters, end'}>
	<ul>
		{#if at === 'top'}
			<li>
				{#if view.kind === 'chapter'}
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- fullHref resolves -->
					<a href={fullHref}>Entire Work</a>
				{:else}
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- chapterHref resolves -->
					<a href={chapterHref(first)}>Chapter by Chapter</a>
				{/if}
			</li>
		{/if}
		{#if place?.previous !== undefined}
			<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- stepHref resolves -->
			<li><a href={stepHref(place.previous)}>← Previous Chapter</a></li>
		{/if}
		{#if at === 'bottom'}
			<li><a href="#top">↑ Top</a></li>
		{/if}
		{#if place?.next !== undefined}
			<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- stepHref resolves -->
			<li><a href={stepHref(place.next)}>Next Chapter →</a></li>
		{/if}
	</ul>
	{#if at === 'top'}
		<details class="index">
			<summary>Chapter Index</summary>
			<ol>
				{#each chapters as c (c.position)}
					<li>
						<!-- The entire work's chapters are on this page, so its index links are fragments. -->
						<!-- eslint-disable svelte/no-navigation-without-resolve -- a fragment, or chapterHref resolves -->
						<a
							href={view.kind === 'full' ? `#chapter-${String(c.position)}` : chapterHref(c.position)}
							aria-current={c.position === current ? 'page' : undefined}>{chapterHeading(c)}</a
						>
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
					</li>
				{/each}
			</ol>
		</details>
	{/if}
</nav>

<style>
	.chapter-nav {
		margin-block: var(--space-3);
	}

	ul {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		justify-content: center;
		padding: 0;
		margin: 0;
		list-style: none;
	}

	.index {
		margin-top: var(--space-2);
		text-align: center;
	}

	.index ol {
		display: inline-block;
		padding-inline-start: var(--space-4);
		text-align: start;
	}

	[aria-current='page'] {
		font-weight: bold;
	}
</style>
