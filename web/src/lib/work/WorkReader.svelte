<!--
	A work's reading view (docs/part4-ui.md stage 8b), as otwarchive's works/show and
	chapters/show: the navigation, the metadata box, the banners, then in #workskin the preface
	(title, byline, and on the first chapter the summary and notes), the chapters, and after the
	last one the work's end notes. The work comes from the layout; this loads the chapter bodies.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { tick } from 'svelte';

	import { type Chapter as ChapterData, getChapter, getWorkChapters } from '#lib/api/api.gen.ts';
	import { problemSlug } from '#lib/api/problem.ts';
	import { describeFailure, unreachable } from '#lib/auth/messages.ts';
	import { skinScopeClass } from '#lib/sanitize/skin.ts';

	import Byline from './Byline.svelte';
	import Chapter from './Chapter.svelte';
	import ChapterNav from './ChapterNav.svelte';
	import { chapterPlace, isChaptered, type ReadingView } from './chapters.ts';
	import { getWorkContext } from './context.ts';
	import { seriesGap, statusNote } from './meta.ts';
	import WorkHtml from './WorkHtml.svelte';
	import WorkMeta from './WorkMeta.svelte';

	let { view }: { view: ReadingView } = $props();

	const ctx = getWorkContext();
	const work = $derived(ctx.work);
	const images = $derived(ctx.images);

	const chaptered = $derived(isChaptered(work.chapters_posted, work.chapters_expected));
	const many = $derived(work.chapters.length > 1);
	const place = $derived(view.kind === 'chapter' ? chapterPlace(work.chapters, view.position) : undefined);
	// The preface's summary and notes go with the first chapter, the work's end notes with the
	// last; the entire work has both.
	const showPreface = $derived(view.kind === 'full' || place?.first === true);
	const showAfterword = $derived(view.kind === 'full' || place?.last === true);
	const lastPosition = $derived(work.chapters.at(-1)?.position);

	const gaps = $derived(
		work.series.flatMap((s) => {
			const gap = seriesGap(s);
			return gap === undefined ? [] : [{ id: s.id, title: s.title, gap }];
		})
	);
	const note = $derived(statusNote(work.status));

	/** Where the work's end notes are: here, or on the last chapter's page. */
	const endNotesHref = $derived(
		showAfterword || lastPosition === undefined
			? '#work_endnotes'
			: `${resolve('/(app)/works/[id=id]/chapters/[position=position]', { id: work.id, position: lastPosition })}#work_endnotes`
	);

	type Text = { kind: 'loading' } | { kind: 'ready'; chapters: ChapterData[] } | { kind: 'failed'; text: string };
	let text: Text = $state({ kind: 'loading' });
	// Counts loads, so a slow answer for a chapter the user has moved on from is dropped.
	let loads = 0;

	async function fallBackToChapters(workId: number, first: number): Promise<void> {
		await goto(resolve('/(app)/works/[id=id]/chapters/[position=position]', { id: workId, position: first }), {
			replace: true,
			state: { workTooLarge: true }
		});
	}

	async function load(workId: number, v: ReadingView, hasChapters: boolean, first: number): Promise<void> {
		const mine = ++loads;
		text = { kind: 'loading' };
		if (!hasChapters) {
			text = { kind: 'ready', chapters: [] }; // a work known only from listings has none
			return;
		}
		try {
			if (v.kind === 'full') {
				const res = await getWorkChapters(workId);
				if (mine !== loads) {
					return;
				}
				if (res.status === 200) {
					text = { kind: 'ready', chapters: res.data.chapters };
				} else if (res.status === 422 && problemSlug(res.data) === 'work-too-large') {
					await fallBackToChapters(workId, first);
					return;
				} else if (res.status === 404) {
					text = { kind: 'failed', text: "This work isn't in your mirror any more." };
				} else if (res.status !== 401) {
					text = { kind: 'failed', text: describeFailure(res, '').text };
				}
			} else {
				const res = await getChapter(workId, v.position);
				if (mine !== loads) {
					return;
				}
				if (res.status === 200) {
					text = { kind: 'ready', chapters: [res.data] };
				} else if (res.status === 404) {
					text = { kind: 'failed', text: "This chapter isn't in your mirror." };
				} else if (res.status !== 401) {
					text = { kind: 'failed', text: describeFailure(res, '').text };
				}
			}
		} catch {
			if (mine === loads) {
				text = { kind: 'failed', text: unreachable };
			}
			return;
		}
		// The router scrolled to the URL's fragment before the text was in; now it is there.
		await tick();
		if (mine === loads && page.url.hash.length > 1) {
			document.getElementById(decodeURIComponent(page.url.hash.slice(1)))?.scrollIntoView();
		}
	}

	// Primitives, so a new view object for the same chapter (a fragment link) loads nothing.
	const kind = $derived(view.kind);
	const position = $derived(view.kind === 'chapter' ? view.position : 0);

	$effect(() => {
		const v: ReadingView = kind === 'full' ? { kind } : { kind, position };
		void load(work.id, v, work.chapters.length > 0, work.chapters[0]?.position ?? 1);
	});
</script>

{#if ctx.skin.kind === 'sheet'}
	<!-- AO3's "Hide Creator's Style", in the work's navigation; here, for this work while it is open. -->
	<p class="style-toggle">
		<button type="button" onclick={() => (ctx.skinShown = !ctx.skinShown)}>
			{ctx.skinShown ? "Hide Creator's Style" : "Show Creator's Style"}
		</button>
	</p>
{/if}

{#if many}
	<ChapterNav workId={work.id} chapters={work.chapters} {view} {place} at="top" />
{/if}

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
	<p class="banner" role="note">This work is only known from a listing so far: its text hasn't been mirrored yet.</p>
{/if}
{#if view.kind === 'chapter' && page.state.workTooLarge === true}
	<p class="banner" role="note">This work is too large to show all at once, so it is shown one chapter at a time.</p>
{/if}
{#if ctx.skin.kind === 'failed'}
	<p class="banner" role="note">This work's style couldn't be loaded, so it is shown without it.</p>
{/if}

<!--
	The skin's rules are scoped to .ao3m-skin-scope (#lib/sanitize/skin.ts), so they can style
	#workskin and what is in it but not the frame, whose paint containment keeps anything the
	skin moves or positions from painting outside the work's text.
-->
<div class="skin-frame">
	<div class={skinScopeClass}>
		<div id="workskin">
			<div class="preface group">
				<header>
					<h1 class="title heading">{work.title}</h1>
					<p class="byline heading">by <Byline creators={work.creators} anonymous={work.anonymous} /></p>
				</header>

				{#if showPreface}
					{#if work.summary_html !== undefined}
						<section class="summary module" aria-labelledby="summary">
							<h2 id="summary">Summary</h2>
							<WorkHtml as="blockquote" html={work.summary_html} {images} />
						</section>
					{/if}
					{#if work.notes_html !== undefined || work.end_notes_html !== undefined}
						<section class="notes module" aria-labelledby="notes">
							<h2 id="notes">Notes</h2>
							{#if work.notes_html !== undefined}
								<WorkHtml as="blockquote" html={work.notes_html} {images} />
							{/if}
							{#if work.end_notes_html !== undefined}
								<p class="jump">
									<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolved above, or a fragment -->
									(See the end of the work for <a href={endNotesHref}
										>{work.notes_html === undefined ? 'notes' : 'more notes'}</a
									>.)
								</p>
							{/if}
						</section>
					{/if}
				{/if}
			</div>

			<div id="chapters">
				{#if text.kind === 'ready'}
					{#each text.chapters as c (c.position)}
						<Chapter chapter={c} workId={work.id} {chaptered} {images} />
					{/each}
				{:else if text.kind === 'loading'}
					<p class="ao3m-muted" aria-live="polite">Loading…</p>
				{:else}
					<p class="ao3m-error" role="alert">{text.text}</p>
				{/if}
			</div>

			{#if showAfterword && work.end_notes_html !== undefined}
				<div class="afterword preface group">
					<section class="end notes module" id="work_endnotes" aria-labelledby="work-endnotes-heading">
						<h2 id="work-endnotes-heading">End Notes</h2>
						<WorkHtml as="blockquote" html={work.end_notes_html} {images} />
					</section>
				</div>
			{/if}
		</div>
	</div>
</div>

{#if many}
	<ChapterNav workId={work.id} chapters={work.chapters} {view} {place} at="bottom" />
{/if}

<style>
	/* Paint containment also makes the frame the containing block of anything the skin
	   positions, and clips what it paints to the frame's box. */
	.skin-frame {
		contain: paint;
	}

	.style-toggle {
		margin-block: var(--space-2) 0;
		text-align: center;
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

	/* The work's own preface groups, set like a chapter's (Chapter.svelte, after AO3's). */
	@media (min-width: 40rem) {
		.preface {
			margin-inline: var(--space-4);
		}
	}

	/* AO3 has no rule above the afterword; this one marks the last chapter's end like the rest. */
	.afterword {
		margin-block: var(--space-4) var(--space-3);
		padding-top: var(--space-2);
		border-top: 1px solid var(--color-muted);
	}

	.preface section h2 {
		padding-bottom: var(--space-1);
		border-bottom: 1px solid var(--color-muted);
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

	.jump {
		color: var(--color-muted);
		font-size: 0.875rem;
	}

	.ao3m-muted {
		color: var(--color-muted);
	}

	.ao3m-error {
		color: var(--color-accent);
	}
</style>
