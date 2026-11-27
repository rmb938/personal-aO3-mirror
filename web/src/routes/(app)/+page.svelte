<!--
	The home page (docs/part4-ui.md stage 8a): every mirrored work, most recently updated first,
	a page at a time. Browsing and search come in Part 4b.
-->
<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import { listWorks, type WorkList } from '#lib/api/api.gen.ts';
	import { describeFailure, unreachable } from '#lib/auth/messages.ts';
	import { parseId } from '#lib/work/meta.ts';
	import WorkBlurb from '#lib/work/WorkBlurb.svelte';

	const perPage = 20;

	let list: WorkList | undefined = $state();
	let error = $state('');

	/** The page number in the URL (?page=N), 1 when absent or not a number. */
	const current = $derived(parseId(page.url.searchParams.get('page') ?? '') ?? 1);
	const pages = $derived(list === undefined ? 1 : Math.max(1, Math.ceil(list.total / perPage)));

	function pageHref(n: number): string {
		return n === 1 ? resolve('/(app)') : `${resolve('/(app)')}?page=${String(n)}`;
	}

	async function load(n: number): Promise<void> {
		error = '';
		try {
			const res = await listWorks({ page: n, per_page: perPage });
			if (n !== current) {
				return; // the user has gone on to another page
			}
			if (res.status === 200) {
				list = res.data;
			} else if (res.status !== 401) {
				// A 401 fired onSessionLost: the layout is on its way to the login page.
				error = describeFailure(res, '').text;
			}
		} catch {
			error = unreachable;
		}
	}

	$effect(() => {
		void load(current);
	});
</script>

<svelte:head>
	<title>ao3mirror</title>
</svelte:head>

<div class="home">
	<h1>Works</h1>
	{#if error !== ''}
		<p class="error" role="alert">{error}</p>
	{:else if list === undefined}
		<p class="muted" aria-live="polite">Loading…</p>
	{:else if list.total === 0}
		<p>Nothing is mirrored yet. Add a source with <code>ao3mirror source add</code>.</p>
	{:else}
		<p class="muted">
			{list.total === 1 ? '1 work' : `${String(list.total)} works`}{#if pages > 1}, page {list.page} of {pages}{/if}
		</p>
		{#each list.items as w (w.id)}
			<WorkBlurb work={w} />
		{/each}
		{#if pages > 1}
			<nav class="pager" aria-label="Pages">
				{#if current > 1}
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- pageHref resolves -->
					<a href={pageHref(current - 1)} rel="prev">← Newer</a>
				{/if}
				{#if current < pages}
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- pageHref resolves -->
					<a href={pageHref(current + 1)} rel="next">Older →</a>
				{/if}
			</nav>
		{/if}
	{/if}
</div>

<style>
	.home {
		max-width: var(--reading-width);
		margin: 0 auto;
	}

	.pager {
		display: flex;
		justify-content: space-between;
		margin-top: var(--space-3);
	}

	/* Alone on the first page, Older still sits on the right. */
	.pager [rel='next'] {
		margin-inline-start: auto;
	}

	.muted {
		color: var(--color-muted);
	}

	.error {
		color: var(--color-accent);
	}
</style>
