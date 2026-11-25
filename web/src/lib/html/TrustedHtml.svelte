<!--
	The only component allowed to use {@html} (eslint.config.js). It takes a TrustedHTML, which
	only the HTML sanitizer's Trusted Types policy (ao3mirror-html, stage 7) creates, so a plain
	string can't reach it: TypeScript rejects one here, and the CSP's
	require-trusted-types-for 'script' makes the browser reject one at runtime.
-->
<script lang="ts">
	let { html }: { html: TrustedHTML } = $props();
</script>

{@html html}
