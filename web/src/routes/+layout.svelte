<script lang="ts">
	import '../app.css';

	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();
</script>

<main>
	{@render children()}
</main>

<style>
	main {
		padding: var(--space-3);
	}
</style>
