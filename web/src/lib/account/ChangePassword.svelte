<!--
	Change the password (docs/part4-ui.md stage 6): the current password, the new one twice and a
	code from the authenticator app. The server signs out every other session; this one stays.
-->
<script lang="ts">
	import { changePassword } from '#lib/api/api.gen.ts';
	import { Cooldown } from '#lib/auth/cooldown.svelte.ts';
	import { describeFailure, passwordPolicy, plural, unreachable } from '#lib/auth/messages.ts';

	let {
		username,
		onChanged
	}: {
		/** For password managers, which file the new password under it. */
		username: string;
		/** The password changed (and the other sessions ended). */
		onChanged: () => void;
	} = $props();

	let current = $state('');
	let next = $state('');
	let confirm = $state('');
	let code = $state('');
	let busy = $state(false);
	let error = $state('');
	let done = $state(false);

	const cooldown = new Cooldown();
	$effect(() => () => {
		cooldown.stop();
	});

	async function submit(event: SubmitEvent): Promise<void> {
		event.preventDefault();
		done = false;
		if (next !== confirm) {
			error = "The new passwords don't match.";
			return;
		}
		busy = true;
		error = '';
		try {
			const res = await changePassword({ current_password: current, password: next, code: code.trim() });
			if (res.status === 204) {
				current = next = confirm = code = '';
				done = true;
				onChanged();
				return;
			}
			if (res.status === 401) {
				return; // the session is gone: the layout is on its way to the login page
			}
			const msg = describeFailure(res, { forbidden: 'The current password or the code is wrong.' });
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

<section aria-labelledby="password-heading">
	<h2 id="password-heading">Change your password</h2>
	<form onsubmit={submit}>
		<input class="hidden" name="username" autocomplete="username" value={username} readonly tabindex="-1" />
		<label>
			Current password
			<input name="current_password" type="password" autocomplete="current-password" required bind:value={current} />
		</label>
		<label>
			New password
			<input
				name="password"
				type="password"
				autocomplete="new-password"
				minlength="8"
				required
				aria-describedby="password-policy"
				bind:value={next}
			/>
		</label>
		<p id="password-policy" class="hint">{passwordPolicy}</p>
		<label>
			New password again
			<input name="confirm" type="password" autocomplete="new-password" required bind:value={confirm} />
		</label>
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
			{cooldown.left > 0 ? `Wait ${plural(cooldown.left, 'second')}` : 'Change password'}
		</button>
	</form>
	{#if done}
		<p role="status">Your password is changed. Your other devices were signed out.</p>
	{/if}
	{#if error !== ''}
		<p class="error" role="alert">{error}</p>
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

	.hidden {
		display: none;
	}

	.hint {
		margin: calc(-1 * var(--space-2)) 0 0;
		color: var(--color-muted);
		font-size: 0.875rem;
	}

	.error {
		color: var(--color-accent);
	}
</style>
