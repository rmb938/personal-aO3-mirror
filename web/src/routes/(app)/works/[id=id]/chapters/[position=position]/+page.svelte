<!--
	One chapter of a work (docs/part4-ui.md stage 8b), as AO3's chapters/show. Chapters are
	addressed by position: a mirrored chapter's AO3 ID can be unknown.
-->
<script lang="ts">
	import WorkReader from '#lib/work/WorkReader.svelte';

	import type { PageProps } from './$types';

	// The `position` matcher (src/params.ts) hands over the position as a number.
	let { params }: PageProps = $props();
</script>

<WorkReader view={{ kind: 'chapter', position: params.position }} />
