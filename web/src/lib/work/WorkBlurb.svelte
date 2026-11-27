<!--
	One work in a listing, like AO3's blurb: title (a link to the work page) and byline, the
	fandoms, the summary and a line of stats. A blurb has no images (the listing doesn't send the
	work's image list), so its summary's images are left out.
-->
<script lang="ts">
	import { resolve } from '$app/paths';

	import type { WorkBlurb } from '#lib/api/api.gen.ts';

	import Byline from './Byline.svelte';
	import { chapterCount } from './meta.ts';
	import WorkHtml from './WorkHtml.svelte';

	let { work }: { work: WorkBlurb } = $props();

	const fandoms = $derived(work.tags.filter((t) => t.type === 'Fandom'));
	const count = new Intl.NumberFormat();
</script>

<article class="blurb" aria-labelledby="blurb-{work.id}">
	<h2 id="blurb-{work.id}">
		<a href={resolve('/(app)/works/[id=id]', { id: work.id })}>{work.title}</a>
	</h2>
	<p class="byline">by <Byline creators={work.creators} anonymous={work.anonymous} /></p>
	{#if fandoms.length > 0}
		<ul class="fandoms" aria-label="Fandoms">
			{#each fandoms as t (t.id)}
				<li>{t.name}</li>
			{/each}
		</ul>
	{/if}
	{#if work.summary_html !== undefined}
		<WorkHtml html={work.summary_html} />
	{/if}
	<p class="stats">
		{#if work.language !== undefined}<span>{work.language}</span>{/if}
		<span>Words: {count.format(work.words)}</span>
		<span>Chapters: {chapterCount(work.chapters_posted, work.chapters_expected)}</span>
		{#if work.revised_on !== undefined}<span>Updated: {work.revised_on}</span>{/if}
	</p>
</article>

<style>
	.blurb {
		padding: var(--space-2) 0 var(--space-3);
		border-bottom: 1px solid var(--color-muted);
	}

	h2 {
		margin: 0;
		font-size: 1.125rem;
	}

	.byline {
		margin: 0;
	}

	.fandoms {
		margin: var(--space-1) 0 0;
		padding: 0;
		list-style: none;
		color: var(--color-muted);
	}

	.fandoms li {
		display: inline;
	}

	.fandoms li:not(:last-child)::after {
		content: ', ';
	}

	.stats {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
		margin: 0;
		color: var(--color-muted);
		font-size: 0.875rem;
	}
</style>
