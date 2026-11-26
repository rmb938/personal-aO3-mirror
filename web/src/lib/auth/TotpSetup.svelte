<!--
	A new TOTP secret to add to an authenticator app (docs/part4-ui.md stage 6b): a QR code of
	the otpauth URI to scan, the same URI as a link for a phone (which can't scan its own
	screen), and the secret as text to type. The secret is never stored in the browser.
-->
<script lang="ts">
	import { groupSecret } from '#lib/auth/enroll.ts';
	import { qr } from '#lib/auth/qr.ts';

	let { secret, uri }: { secret: string; uri: string } = $props();

	let code = $derived(qr(uri));
	let copied = $state('');

	async function copy(): Promise<void> {
		try {
			await navigator.clipboard.writeText(secret);
			copied = 'Copied.';
		} catch {
			copied = "Copying isn't allowed here. Type the key instead.";
		}
	}
</script>

<section class="setup" aria-labelledby="totp-setup-heading">
	<h2 id="totp-setup-heading">Add this account to your authenticator app</h2>
	<p>Scan the code with your authenticator app, such as a password manager or Google Authenticator.</p>
	<svg
		class="qr"
		viewBox="0 0 {code.size} {code.size}"
		role="img"
		aria-label="QR code of the new authenticator key"
		shape-rendering="crispEdges"
	>
		<rect width={code.size} height={code.size} class="light" />
		<path d={code.path} class="dark" />
	</svg>
	<p>
		<!-- An otpauth: link opens the authenticator app; it is not an app route. -->
		<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
		On this phone? <a href={uri}>Open it in your authenticator app</a>.
	</p>
	<p>Or type this key into the app:</p>
	<p class="secret"><code aria-label="Authenticator key">{groupSecret(secret)}</code></p>
	<div class="actions">
		<button type="button" onclick={copy}>Copy key</button>
	</div>
	{#if copied !== ''}
		<p class="hint" aria-live="polite">{copied}</p>
	{/if}
</section>

<style>
	.setup {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}

	h2 {
		margin: 0;
		font-size: 1.25rem;
	}

	p {
		margin: 0;
	}

	/* Dark on light in both themes: not every scanner reads an inverted code. */
	.qr {
		width: min(100%, 14rem);
		height: auto;
		align-self: center;
	}

	.light {
		fill: #fff;
	}

	.dark {
		fill: #000;
	}

	.secret code {
		font-size: 1.125rem;
		/* Groups wrap whole on a phone, never mid-group. */
		word-spacing: 0.25rem;
	}

	.actions button {
		font: inherit;
		padding: var(--space-2) var(--space-3);
	}

	.hint {
		color: var(--color-muted);
		font-size: 0.875rem;
	}
</style>
