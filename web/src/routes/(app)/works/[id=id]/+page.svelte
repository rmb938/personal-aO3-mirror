<!--
	A work's page (docs/part4-ui.md stage 8b): the entire work, as AO3's works/show. Like AO3, a
	work of several chapters opens at its first chapter unless ?view_full_work=true asks for all
	of them; a one-shot is read here.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import { fullWorkParam } from '#lib/work/chapters.ts';
	import { getWorkContext } from '#lib/work/context.ts';
	import WorkReader from '#lib/work/WorkReader.svelte';

	const ctx = getWorkContext();
	const first = $derived(ctx.work.chapters.length > 1 ? ctx.work.chapters[0]?.position : undefined);
	const full = $derived(page.url.searchParams.get(fullWorkParam) === 'true');
	const toFirst = $derived(full ? undefined : first);

	$effect(() => {
		if (toFirst !== undefined) {
			void goto(resolve('/(app)/works/[id=id]/chapters/[position=position]', { id: ctx.work.id, position: toFirst }), {
				replace: true
			});
		}
	});
</script>

{#if toFirst === undefined}
	<WorkReader view={{ kind: 'full' }} />
{/if}
