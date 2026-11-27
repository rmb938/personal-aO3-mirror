<!--
	A work's page (docs/part4-ui.md stage 8a): its title and byline, the metadata box, a note for
	each partly mirrored series, the summary, notes, chapter list and end notes. Reading the
	chapters comes in 8b, the work skin in 8c.
-->
<script lang="ts">
	import { getWork, type Work } from '#lib/api/api.gen.ts';
	import { describeFailure, unreachable } from '#lib/auth/messages.ts';
	import { imageMap } from '#lib/sanitize/html.ts';
	import Byline from '#lib/work/Byline.svelte';
	import { seriesGap, statusNote } from '#lib/work/meta.ts';
	import WorkHtml from '#lib/work/WorkHtml.svelte';
	import WorkMeta from '#lib/work/WorkMeta.svelte';

	import type { PageProps } from './$types';

	// The `id` matcher (src/params.ts) hands over the work ID as a number.
	let { params }: PageProps = $props();

	/** Shown for a 404: the route matched an ID, but the mirror has no such work. */
	const notMirrored = "This work isn't in your mirror.";

	let work: Work | undefined = $state();
	let error = $state('');

	const images = $derived(work === undefined ? new Map<string, string>() : imageMap(work.images));
	const gaps = $derived(
		work === undefined
			? []
			: work.series.flatMap((s) => {
					const gap = seriesGap(s);
					return gap === undefined ? [] : [{ id: s.id, title: s.title, gap }];
				})
	);

	async function load(id: number): Promise<void> {
		work = undefined;
		error = '';
		try {
			const res = await getWork(id);
			if (id !== params.id) {
				return; // the user has gone on to another work
			}
			if (res.status === 200) {
				work = res.data;
			} else if (res.status === 404) {
				error = notMirrored;
			} else if (res.status !== 401) {
				// A 401 fired onSessionLost: the layout is on its way to the login page.
				error = describeFailure(res, '').text;
			}
		} catch {
			error = unreachable;
		}
	}

	$effect(() => {
		void load(params.id);
	});
</script>

<svelte:head>
	<title>{work === undefined ? 'Work' : work.title} · ao3mirror</title>
</svelte:head>

<article class="work">
	{#if work !== undefined}
		{@const note = statusNote(work.status)}
		<WorkMeta {work} />

		{#each gaps as g (g.id)}
			<p class="banner" role="note">
				This work is in <span class="series-title">{g.title}</span>, which is only partly mirrored:
				{g.gap}
			</p>
		{/each}
		{#if note !== undefined}
			<p class="banner" role="note">{note}</p>
		{/if}
		{#if work.last_fetched_at === undefined}
			<p class="banner" role="note">
				This work is only known from a listing so far: its text hasn't been mirrored yet.
			</p>
		{/if}

		<header>
			<h1>{work.title}</h1>
			<p class="byline">by <Byline creators={work.creators} anonymous={work.anonymous} /></p>
		</header>

		{#if work.summary_html !== undefined}
			<section aria-labelledby="summary">
				<h2 id="summary">Summary</h2>
				<WorkHtml html={work.summary_html} {images} />
			</section>
		{/if}
		{#if work.notes_html !== undefined}
			<section aria-labelledby="notes">
				<h2 id="notes">Notes</h2>
				<WorkHtml html={work.notes_html} {images} />
			</section>
		{/if}
		{#if work.chapters.length > 0}
			<section aria-labelledby="chapters">
				<h2 id="chapters">Chapters</h2>
				<ol class="chapters">
					{#each work.chapters as c (c.position)}
						<li>
							Chapter {c.position}{#if c.title !== undefined && c.title !== ''}: {c.title}{/if}
							{#if c.published_on !== undefined}
								<span class="muted">({c.published_on})</span>
							{/if}
						</li>
					{/each}
				</ol>
			</section>
		{/if}
		{#if work.end_notes_html !== undefined}
			<section aria-labelledby="end-notes">
				<h2 id="end-notes">End Notes</h2>
				<WorkHtml html={work.end_notes_html} {images} />
			</section>
		{/if}
	{:else if error === ''}
		<p class="muted" aria-live="polite">Loading…</p>
	{:else}
		<p class="error" role="alert">{error}</p>
	{/if}
</article>

<style>
	.work {
		max-width: var(--reading-width);
		margin: 0 auto;
	}

	header {
		margin-top: var(--space-4);
		text-align: center;
	}

	h1 {
		margin-bottom: var(--space-1);
		font-family: var(--font-reading);
	}

	.byline {
		margin-top: 0;
	}

	h2 {
		font-size: 1.125rem;
	}

	.banner {
		padding: var(--space-2) var(--space-3);
		border-inline-start: 4px solid var(--color-accent);
		background: color-mix(in srgb, var(--color-accent) 8%, transparent);
	}

	.series-title {
		font-style: italic;
	}

	.chapters {
		padding-inline-start: 0;
		list-style: none;
	}

	.muted {
		color: var(--color-muted);
	}

	.error {
		color: var(--color-accent);
	}
</style>
