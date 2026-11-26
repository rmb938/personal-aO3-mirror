<!--
	Replace the recovery codes (docs/part4-ui.md stage 6): with a code from the authenticator app,
	the server makes 10 new ones, shown once, and the old ones stop working.
-->
<script lang="ts">
	import { regenerateRecoveryCodes } from '#lib/api/api.gen.ts';
	import { Cooldown } from '#lib/auth/cooldown.svelte.ts';
	import { describeFailure, plural, unreachable } from '#lib/auth/messages.ts';
	import RecoveryCodes from '#lib/auth/RecoveryCodes.svelte';

	let {
		username,
		left,
		onReplaced
	}: {
		username: string;
		/** How many of the current codes are unused. */
		left: number;
		/** New codes were made (the count of unused ones changed). */
		onReplaced: () => void;
	} = $props();

	let code = $state('');
	let busy = $state(false);
	let error = $state('');
	let codes: string[] | undefined = $state();

	const cooldown = new Cooldown();
	$effect(() => () => {
		cooldown.stop();
	});

	async function submit(event: SubmitEvent): Promise<void> {
		event.preventDefault();
		busy = true;
		error = '';
		try {
			const res = await regenerateRecoveryCodes({ code: code.trim() });
			if (res.status === 200) {
				code = '';
				codes = res.data.recovery_codes;
				onReplaced();
				return;
			}
			if (res.status === 401) {
				return; // the session is gone: the layout is on its way to the login page
			}
			const msg = describeFailure(res, { forbidden: "That code didn't work. Check your authenticator app." });
			error = msg.text;
			if (msg.retryAfter !== undefined) {
				cooldown.start(msg.retryAfter);
			}
		} catch {
			error = unreachable;
		} finally {
			busy = false;
		}
	}
</script>

<section aria-labelledby="recovery-heading">
	<h2 id="recovery-heading">Recovery codes</h2>
	{#if codes !== undefined}
		<RecoveryCodes
			{codes}
			{username}
			onDone={() => {
				codes = undefined;
			}}
		/>
	{:else}
		<p class:warn={left <= 3}>
			You have {plural(left, 'unused code')} left.
			{#if left <= 3}Make new ones before you run out.{/if}
		</p>
		<form onsubmit={submit}>
			<p class="hint">Making new codes stops all of your current ones from working.</p>
			<label>
				Code from your authenticator app
				<input
					name="code"
					inputmode="numeric"
					autocomplete="one-time-code"
					pattern={'[0-9]{6}'}
					maxlength="6"
					required
					bind:value={code}
				/>
			</label>
			<button type="submit" disabled={busy || cooldown.left > 0}>
				{cooldown.left > 0 ? `Wait ${plural(cooldown.left, 'second')}` : 'Make new recovery codes'}
			</button>
		</form>
		{#if error !== ''}
			<p class="error" role="alert">{error}</p>
		{/if}
	{/if}
</section>

<style>
	form {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		max-width: 22rem;
	}

	label {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}

	input,
	button {
		font: inherit;
		padding: var(--space-2);
	}

	.hint {
		margin: 0;
		color: var(--color-muted);
		font-size: 0.875rem;
	}

	.warn,
	.error {
		color: var(--color-accent);
	}
</style>
