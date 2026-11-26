<!--
	New recovery codes, shown once (docs/part4-ui.md stage 6): the server never shows them again,
	so the user copies or downloads them before going on. They are account secrets, not fanwork;
	they are never stored in the browser, only offered as a download built in memory.
-->
<script lang="ts">
	let {
		codes,
		username,
		onDone
	}: {
		codes: string[];
		username: string;
		/** The user has saved them: the parent stops showing them. */
		onDone: () => void;
	} = $props();

	let copied = $state('');

	let text = $derived(
		`ao3mirror recovery codes for ${username}. Each one signs you in once, without your authenticator app.\n\n` +
			codes.join('\n') +
			'\n'
	);

	// The download's blob: URL, released when the codes go away.
	let href = $derived(URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' })));
	$effect(() => {
		const url = href;
		return () => {
			URL.revokeObjectURL(url);
		};
	});

	async function copy(): Promise<void> {
		try {
			await navigator.clipboard.writeText(codes.join('\n'));
			copied = 'Copied.';
		} catch {
			copied = "Copying isn't allowed here. Download them or write them down instead.";
		}
	}
</script>

<section class="codes" aria-labelledby="recovery-codes-heading">
	<h3 id="recovery-codes-heading">Your recovery codes</h3>
	<p>
		Save these somewhere safe, such as a password manager. Each one signs you in once if you lose your
		authenticator app. <strong>They won't be shown again.</strong>
	</p>
	<ol aria-label="Recovery codes">
		{#each codes as code (code)}
			<li><code>{code}</code></li>
		{/each}
	</ol>
	<div class="actions">
		<button type="button" onclick={copy}>Copy</button>
		<!-- A blob: URL of the file above, not an app route. -->
		<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
		<a class="button" {href} download="ao3mirror-{username}-recovery-codes.txt">Download</a>
		<button type="button" onclick={onDone}>I've saved them</button>
	</div>
	{#if copied !== ''}
		<p class="hint" aria-live="polite">{copied}</p>
	{/if}
</section>

<style>
	.codes {
		padding: var(--space-3);
		border: 2px solid var(--color-accent);
	}

	h3 {
		margin-top: 0;
	}

	ol {
		display: grid;
		/* One column on a phone, two or more once a code fits beside another. */
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 20rem), 1fr));
		gap: var(--space-1) var(--space-3);
		padding-left: var(--space-4);
	}

	code {
		font-size: 1rem;
		/* A code is copied by eye: never break it across lines. */
		white-space: nowrap;
	}

	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}

	button,
	.button {
		font: inherit;
		padding: var(--space-2) var(--space-3);
	}

	/* The download link, made to look like the buttons beside it (the browser's own button colors). */
	.button {
		border: 1px solid ButtonBorder;
		border-radius: 2px;
		background: ButtonFace;
		color: ButtonText;
		text-decoration: none;
	}

	.hint {
		color: var(--color-muted);
		font-size: 0.875rem;
	}
</style>
