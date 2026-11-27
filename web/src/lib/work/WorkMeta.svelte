<!--
	The box at the top of a work page, as on AO3: the tags by type, the language, series and
	collections, then the stats. Tags, series and collections are plain text for now: their
	pages come in Part 4b.
-->
<script lang="ts">
	import type { Work } from '#lib/api/api.gen.ts';

	import { chapterCount, groupTags, revisedLabel } from './meta.ts';

	let { work }: { work: Work } = $props();

	const count = new Intl.NumberFormat();

	const groups = $derived(groupTags(work.tags));
	/** The stats line's label and value pairs, in AO3's order. */
	const stats = $derived.by(() => {
		const out: [string, string][] = [];
		if (work.published_on !== undefined) {
			out.push(['Published', work.published_on]);
		}
		const revised = revisedLabel(work);
		if (revised !== undefined && work.revised_on !== undefined) {
			out.push([revised, work.revised_on]);
		}
		out.push(
			['Words', count.format(work.words)],
			['Chapters', chapterCount(work.chapters_posted, work.chapters_expected)],
			['Kudos', count.format(work.stats.kudos)],
			['Hits', count.format(work.stats.hits)],
			['Comments', count.format(work.stats.comments)],
			['Bookmarks', count.format(work.stats.bookmarks)]
		);
		return out;
	});
</script>

<dl class="meta">
	{#each groups as g (g.type)}
		<dt>{g.label}:</dt>
		<dd>
			<ul class="tags" aria-label={g.label}>
				{#each g.tags as t (t.id)}
					<li>{t.name}</li>
				{/each}
			</ul>
		</dd>
	{/each}
	{#if work.language !== undefined}
		<dt>Language:</dt>
		<dd>{work.language}</dd>
	{/if}
	{#if work.series.length > 0}
		<dt>Series:</dt>
		<dd>
			<ul class="plain">
				{#each work.series as s (s.id)}
					<li>
						{#if s.position !== undefined}Part {s.position} of{/if}
						<span class="series-title">{s.title}</span>
					</li>
				{/each}
			</ul>
		</dd>
	{/if}
	{#if work.collections.length > 0}
		<dt>Collections:</dt>
		<dd>
			<ul class="tags" aria-label="Collections">
				{#each work.collections as c (c.id)}
					<li>{c.title}</li>
				{/each}
			</ul>
		</dd>
	{/if}
	<dt>Stats:</dt>
	<dd>
		<!-- Each pair in a div, so a label never wraps away from its value. -->
		<dl class="stats">
			{#each stats as [label, value] (label)}
				<div>
					<dt>{label}:</dt>
					<dd>{value}</dd>
				</div>
			{/each}
		</dl>
	</dd>
</dl>

<style>
	.meta {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: var(--space-1) var(--space-2);
		margin: 0;
		padding: var(--space-2) var(--space-3);
		border: 1px solid var(--color-muted);
	}

	/* On a phone the labels go above their values, so long tag lists get the whole width. */
	@media (max-width: 30rem) {
		.meta {
			grid-template-columns: 1fr;
		}

		.meta > dd {
			margin-bottom: var(--space-1);
		}
	}

	.meta > dt {
		font-weight: bold;
	}

	dd {
		margin: 0;
	}

	ul {
		margin: 0;
		padding: 0;
		list-style: none;
	}

	/* Tags run on as one comma-separated line, like AO3's. */
	.tags li {
		display: inline;
	}

	.tags li:not(:last-child)::after {
		content: ', ';
	}

	.series-title {
		font-style: italic;
	}

	.stats {
		display: flex;
		flex-wrap: wrap;
		gap: 0 var(--space-3);
		margin: 0;
	}

	.stats div {
		display: flex;
		gap: var(--space-1);
		white-space: nowrap;
	}

	.stats dt {
		color: var(--color-muted);
	}
</style>
