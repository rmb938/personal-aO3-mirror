<!--
	A work's creators in byline order, or Anonymous. Plain text: creator pages come in Part 4b.
-->
<script lang="ts">
	import type { WorkCreator } from '#lib/api/api.gen.ts';

	import { creatorName } from './meta.ts';

	let { creators, anonymous }: { creators: WorkCreator[]; anonymous: boolean } = $props();
</script>

{anonymous || creators.length === 0 ? 'Anonymous' : creators.map(creatorName).join(', ')}
