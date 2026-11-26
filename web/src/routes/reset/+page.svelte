<!--
	Reset from a reset link (docs/part4-ui.md stage 6b). The fragment holds the token and the
	link's scope, which says whether to ask for a new password (the server checks it against the
	link's own scope). Then a TOTP code: from a new secret when the reset replaces it, else from
	the current one. A reset that replaced the secret ends with new recovery codes, shown once.
	Every session of the account ends, so the user then signs in again.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import { confirmReset, type Enrollment, startReset } from '#lib/api/api.gen.ts';
	import { Cooldown } from '#lib/auth/cooldown.svelte.ts';
	import { otpauthUsername, readLink, scopeHasPassword } from '#lib/auth/enroll.ts';
	import { describeFailure, passwordPolicy, plural, unreachable } from '#lib/auth/messages.ts';
	import RecoveryCodes from '#lib/auth/RecoveryCodes.svelte';
	import TotpSetup from '#lib/auth/TotpSetup.svelte';

	const unusableLink = "This reset link can't be used: it has expired, was used already or was revoked. Ask for a new one.";

	let link = $derived(readLink(page.url.hash, 'reset'));
	let askPassword = $derived(link?.scope !== undefined && scopeHasPassword(link.scope));

	let password = $state('');
	let confirm = $state('');
	let code = $state('');

	let enrollment: Enrollment | undefined = $state();
	// The result: new recovery codes (and whose), or none when the reset kept the current ones.
	let done: { codes: string[]; username: string } | undefined = $state();
	let error = $state('');
	let busy = $state(false);

	const cooldown = new Cooldown();
	$effect(() => () => {
		cooldown.stop();
	});

	function fail(text: string, retryAfter?: number): void {
		error = text;
		if (retryAfter !== undefined) {
			cooldown.start(retryAfter);
		}
	}

	async function submitStart(event: SubmitEvent): Promise<void> {
		event.preventDefault();
		if (link === undefined) {
			return;
		}
		if (askPassword && password !== confirm) {
			error = "The passwords don't match.";
			return;
		}
		busy = true;
		error = '';
		try {
			const res = await startReset({ invite_token: link.token, ...(askPassword ? { password } : {}) });
			if (res.status === 200) {
				enrollment = res.data;
				password = confirm = '';
				return;
			}
			const msg = describeFailure(res, unusableLink);
			fail(msg.text, msg.retryAfter);
		} catch {
			fail(unreachable);
		} finally {
			busy = false;
		}
	}

	async function submitCode(event: SubmitEvent): Promise<void> {
		event.preventDefault();
		if (enrollment === undefined) {
			return;
		}
		if (Date.parse(enrollment.expires_at) <= Date.now()) {
			startOver('That took too long. Start the reset again.');
			return;
		}
		busy = true;
		error = '';
		try {
			const res = await confirmReset({ enrollment_token: enrollment.enrollment_token, code: code.trim() });
			if (res.status === 200) {
				const uri = enrollment.otpauth_uri;
				done = { codes: res.data.recovery_codes, username: (uri && otpauthUsername(uri)) ?? 'account' };
				enrollment = undefined;
				return;
			}
			const msg = describeFailure(
				res,
				enrollment.totp_secret === undefined
					? "That code didn't work. Check your authenticator app."
					: "That code didn't work. Check that it comes from the entry you just added, and that your device's clock is right."
			);
			fail(msg.text, msg.retryAfter);
		} catch {
			fail(unreachable);
		} finally {
			busy = false;
		}
	}

	function startOver(text = ''): void {
		enrollment = undefined;
		code = '';
		error = text;
	}
</script>

<svelte:head>
	<title>Reset your account · ao3mirror</title>
</svelte:head>

<div class="enroll">
	<h1>Reset your account</h1>

	{#if link === undefined}
		<p class="error" role="alert">
			This reset link is incomplete. Open the whole link you were given; it ends with a long code after
			<code>#token=</code> and the reset's scope.
		</p>
	{:else if done !== undefined}
		<p role="status">Your account is reset. All your devices were signed out.</p>
		{#if done.codes.length > 0}
			<RecoveryCodes
				codes={done.codes}
				username={done.username}
				onDone={() => {
					void goto(resolve('/login'));
				}}
			/>
		{:else}
			<p><a href={resolve('/login')}>Sign in</a> with your new password.</p>
		{/if}
	{:else if enrollment === undefined}
		<form onsubmit={submitStart}>
			{#if link.scope === 'both'}
				<p>This link sets a new password and a new authenticator app for your account.</p>
			{:else if link.scope === 'password'}
				<p>This link sets a new password for your account. You keep your authenticator app.</p>
			{:else}
				<p>This link sets up a new authenticator app for your account. You keep your password.</p>
			{/if}
			{#if askPassword}
				<label>
					New password
					<input
						name="password"
						type="password"
						autocomplete="new-password"
						minlength="8"
						required
						aria-describedby="password-policy"
						bind:value={password}
					/>
				</label>
				<p id="password-policy" class="hint">{passwordPolicy}</p>
				<label>
					New password again
					<input name="confirm" type="password" autocomplete="new-password" required bind:value={confirm} />
				</label>
			{/if}
			<button type="submit" disabled={busy || cooldown.left > 0}>
				{cooldown.left > 0 ? `Wait ${plural(cooldown.left, 'second')}` : 'Continue'}
			</button>
		</form>
	{:else}
		{#if enrollment.totp_secret !== undefined && enrollment.otpauth_uri !== undefined}
			<TotpSetup secret={enrollment.totp_secret} uri={enrollment.otpauth_uri} />
		{/if}
		<form onsubmit={submitCode}>
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
				{cooldown.left > 0 ? `Wait ${plural(cooldown.left, 'second')}` : 'Reset account'}
			</button>
		</form>
		<div class="alternatives">
			<button
				type="button"
				class="link"
				onclick={() => {
					startOver();
				}}>Start over</button
			>
		</div>
	{/if}

	{#if error !== ''}
		<p class="error" role="alert">{error}</p>
	{/if}
</div>

<style>
	.enroll {
		max-width: 26rem;
		margin: var(--space-4) auto;
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}

	h1,
	p {
		margin: 0;
	}

	form {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}

	label {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}

	input,
	button[type='submit'] {
		font: inherit;
		padding: var(--space-2);
	}

	.hint {
		margin: calc(-1 * var(--space-2)) 0 0;
		color: var(--color-muted);
		font-size: 0.875rem;
	}

	.link {
		padding: 0;
		border: none;
		background: none;
		color: var(--color-accent);
		font: inherit;
		text-decoration: underline;
		cursor: pointer;
	}

	.error {
		color: var(--color-accent);
	}
</style>
