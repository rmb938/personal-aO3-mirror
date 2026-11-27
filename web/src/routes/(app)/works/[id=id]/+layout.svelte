<!--
	A work's pages (docs/part4-ui.md stage 8): this layout loads the work once and hands it to the
	view below it (the entire work, or one chapter), so moving between chapters fetches only the
	chapter.
-->
<script lang="ts">
	import { getWork, type Work } from '#lib/api/api.gen.ts';
	import { describeFailure, unreachable } from '#lib/auth/messages.ts';
	import { imageMap } from '#lib/sanitize/html.ts';
	import { setWorkContext } from '#lib/work/context.ts';

	import type { LayoutProps } from './$types';

	// The `id` matcher (src/params.ts) hands over the work ID as a number.
	let { params, children }: LayoutProps = $props();

	/** Shown for a 404: the route matched an ID, but the mirror has no such work. */
	const notMirrored = "This work isn't in your mirror.";

	// The ID alone, so moving to another chapter of the same work doesn't load it again.
	const id = $derived(params.id);
	let work: Work | undefined = $state();
	let error = $state('');
	const images = $derived(work === undefined ? new Map<string, string>() : imageMap(work.images));

	setWorkContext({
		get work(): Work {
			if (work === undefined) {
				throw new Error('the work view rendered before its work loaded');
			}
			return work;
		},
		get images() {
			return images;
		}
	});

	async function load(want: number): Promise<void> {
		work = undefined;
		error = '';
		try {
			const res = await getWork(want);
			if (want !== id) {
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
		void load(id);
	});
</script>

<svelte:head>
	<title>{work === undefined ? 'Work' : work.title} · ao3mirror</title>
</svelte:head>

<article class="work">
	{#if work !== undefined}
		{@render children()}
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

	.muted {
		color: var(--color-muted);
	}

	.error {
		color: var(--color-accent);
	}
</style>
