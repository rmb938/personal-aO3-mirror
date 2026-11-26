<!--
	Signup from an invite link (docs/part4-ui.md stage 6b): the link's fragment holds the token,
	so it never reaches the server's logs. Three steps: a username and password, then the new
	TOTP secret with a code from it, then the recovery codes, shown once. Then the user signs in.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import { confirmSignup, type Enrollment, signup } from '#lib/api/api.gen.ts';
	import { Cooldown } from '#lib/auth/cooldown.svelte.ts';
	import { readLink } from '#lib/auth/enroll.ts';
	import { describeFailure, passwordPolicy, plural, unreachable, usernameRule } from '#lib/auth/messages.ts';
	import RecoveryCodes from '#lib/auth/RecoveryCodes.svelte';
	import TotpSetup from '#lib/auth/TotpSetup.svelte';

	const unusableInvite = "This invite link can't be used: it has expired, was used already or was revoked. Ask for a new one.";

	let link = $derived(readLink(page.url.hash, 'signup'));

	let username = $state('');
	let password = $state('');
	let confirm = $state('');
	let code = $state('');

	let enrollment: Enrollment | undefined = $state();
	let codes: string[] | undefined = $state();
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

	async function submitAccount(event: SubmitEvent): Promise<void> {
		event.preventDefault();
		if (link === undefined) {
			return;
		}
		if (password !== confirm) {
			error = "The passwords don't match.";
			return;
		}
		busy = true;
		error = '';
		try {
			const res = await signup({ invite_token: link.token, username, password });
			if (res.status === 200) {
				enrollment = res.data;
				password = confirm = '';
				return;
			}
			const msg = describeFailure(res, unusableInvite);
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
			startOver('That took too long. Choose your password again, then add the new key to your app.');
			return;
		}
		busy = true;
		error = '';
		try {
			const res = await confirmSignup({ enrollment_token: enrollment.enrollment_token, code: code.trim() });
			if (res.status === 201) {
				codes = res.data.recovery_codes;
				enrollment = undefined;
				return;
			}
			const msg = describeFailure(
				res,
				"That code didn't work. Check that it comes from the entry you just added, and that your device's clock is right."
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
	<title>Create your account · ao3mirror</title>
</svelte:head>

<div class="enroll">
	<h1>Create your account</h1>

	{#if link === undefined}
		<p class="error" role="alert">
			This invite link is incomplete. Open the whole link you were given; it ends with a long code after
			<code>#token=</code>.
		</p>
	{:else if codes !== undefined}
		<p role="status">Your account is ready.</p>
		<RecoveryCodes
			{codes}
			username={username.toLowerCase()}
			onDone={() => {
				void goto(resolve('/login'));
			}}
		/>
	{:else if enrollment === undefined}
		<form onsubmit={submitAccount}>
			<label>
				Username
				<input
					name="username"
					autocomplete="username"
					autocapitalize="none"
					spellcheck="false"
					pattern={'[A-Za-z0-9_\\-]{3,32}'}
					required
					aria-describedby="username-rule"
					bind:value={username}
				/>
			</label>
			<p id="username-rule" class="hint">{usernameRule}</p>
			<label>
				Password
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
				Password again
				<input name="confirm" type="password" autocomplete="new-password" required bind:value={confirm} />
			</label>
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
				{cooldown.left > 0 ? `Wait ${plural(cooldown.left, 'second')}` : 'Create account'}
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

	h1 {
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
		margin: 0;
		color: var(--color-accent);
	}
</style>
