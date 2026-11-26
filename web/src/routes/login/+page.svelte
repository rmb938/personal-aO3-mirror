<!--
	Two-step login (docs/part4-ui.md stage 5b): username and password, then a TOTP code or a
	recovery code. The second step starts a cookie session, then the page goes back to where the
	user was (the `next` parameter, see #lib/auth/next.ts).
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';

	import { type LoginChallenge, login, loginTOTP } from '#lib/api/api.gen.ts';
	import { Cooldown } from '#lib/auth/cooldown.svelte.ts';
	import { describeFailure, plural, unreachable } from '#lib/auth/messages.ts';
	import { nextParam, safeNext } from '#lib/auth/next.ts';

	// The API's MaxChallengeAttempts: after this many wrong codes the challenge is spent.
	const challengeTries = 5;

	let username = $state('');
	let password = $state('');
	let code = $state('');
	let recoveryCode = $state('');
	let useRecovery = $state(false);

	let challenge: LoginChallenge | undefined = $state();
	let wrongCodes = 0;
	let error = $state('');
	let busy = $state(false);

	// A rate limit or backoff: the submit buttons stay off until it passes.
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

	function startOver(text = ''): void {
		challenge = undefined;
		wrongCodes = 0;
		code = '';
		recoveryCode = '';
		error = text;
	}

	async function submitPassword(event: SubmitEvent): Promise<void> {
		event.preventDefault();
		busy = true;
		error = '';
		try {
			const res = await login({ username, password });
			if (res.status === 200) {
				challenge = res.data;
				password = '';
				return;
			}
			const msg = describeFailure(res, "That username and password don't match.");
			fail(msg.text, msg.retryAfter);
		} catch {
			fail(unreachable);
		} finally {
			busy = false;
		}
	}

	async function submitCode(event: SubmitEvent): Promise<void> {
		event.preventDefault();
		if (challenge === undefined) {
			return;
		}
		if (Date.parse(challenge.expires_at) <= Date.now()) {
			startOver('That sign-in took too long. Enter your password again.');
			return;
		}
		busy = true;
		error = '';
		try {
			const factor = useRecovery ? { recovery_code: recoveryCode.trim() } : { code: code.trim() };
			const res = await loginTOTP({ challenge_token: challenge.challenge_token, ...factor, transport: 'cookie' });
			if (res.status === 200) {
				const next = safeNext(page.url.searchParams.get(nextParam), page.url.origin);
				// safeNext only returns paths on this origin.
				// eslint-disable-next-line svelte/no-navigation-without-resolve
				await goto(next, { replace: true });
				return;
			}
			const msg = describeFailure(
				res,
				useRecovery ? "That recovery code didn't work." : "That code didn't work. Check your authenticator app."
			);
			if (res.status === 401) {
				wrongCodes++;
				if (wrongCodes >= challengeTries) {
					startOver('Too many wrong codes. Enter your password again.');
					return;
				}
			}
			fail(msg.text, msg.retryAfter);
		} catch {
			fail(unreachable);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>Sign in · ao3mirror</title>
</svelte:head>

<div class="login">
	<h1>Sign in</h1>

	{#if challenge === undefined}
		<form onsubmit={submitPassword}>
			<label>
				Username
				<input
					name="username"
					autocomplete="username"
					autocapitalize="none"
					spellcheck="false"
					required
					bind:value={username}
				/>
			</label>
			<label>
				Password
				<input name="password" type="password" autocomplete="current-password" required bind:value={password} />
			</label>
			<button type="submit" disabled={busy || cooldown.left > 0}>
				{cooldown.left > 0 ? `Wait ${plural(cooldown.left, 'second')}` : 'Continue'}
			</button>
		</form>
	{:else}
		<form onsubmit={submitCode}>
			{#if useRecovery}
				<label>
					Recovery code
					<input
						name="recovery_code"
						autocomplete="off"
						autocapitalize="none"
						spellcheck="false"
						required
						bind:value={recoveryCode}
					/>
				</label>
				<p class="hint">Each recovery code works once.</p>
			{:else}
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
			{/if}
			<button type="submit" disabled={busy || cooldown.left > 0}>
				{cooldown.left > 0 ? `Wait ${plural(cooldown.left, 'second')}` : 'Sign in'}
			</button>
		</form>
		<div class="alternatives">
			<button
				type="button"
				class="link"
				onclick={() => {
					useRecovery = !useRecovery;
					error = '';
				}}
			>
				{useRecovery ? 'Use a code from your authenticator app' : 'Use a recovery code instead'}
			</button>
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
	.login {
		max-width: 22rem;
		margin: var(--space-4) auto;
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

	input {
		font: inherit;
		padding: var(--space-2);
	}

	button[type='submit'] {
		font: inherit;
		padding: var(--space-2);
	}

	.hint {
		margin: 0;
		color: var(--color-muted);
		font-size: 0.875rem;
	}

	.alternatives {
		display: flex;
		flex-wrap: wrap;
		justify-content: space-between;
		gap: var(--space-2);
		margin-top: var(--space-3);
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
