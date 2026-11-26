<!--
	The account page (docs/part4-ui.md stage 6): who you are, where you're signed in, and changing
	the password or the recovery codes.
-->
<script lang="ts">
	import ChangePassword from '#lib/account/ChangePassword.svelte';
	import NewRecoveryCodes from '#lib/account/NewRecoveryCodes.svelte';
	import Sessions from '#lib/account/Sessions.svelte';
	import { type Account, getAccount } from '#lib/api/api.gen.ts';
	import { describeFailure, unreachable } from '#lib/auth/messages.ts';
	import { formatTime } from '#lib/format.ts';

	let account: Account | undefined = $state();
	let error = $state('');
	// Bumped when the password changes, which ends the other sessions: the list loads again.
	let passwordChanges = $state(0);

	async function load(): Promise<void> {
		try {
			const res = await getAccount();
			if (res.status === 200) {
				account = res.data;
				error = '';
			} else if (res.status !== 401) {
				error = describeFailure(res, '').text;
			}
		} catch {
			error = unreachable;
		}
	}

	$effect(() => {
		void load();
	});
</script>

<svelte:head>
	<title>Account · ao3mirror</title>
</svelte:head>

<div class="account">
	<h1>Account</h1>
	{#if account !== undefined}
		<dl>
			<dt>Username</dt>
			<dd>{account.username}</dd>
			<dt>Member since</dt>
			<dd>{formatTime(account.created_at)}</dd>
			{#if account.password_changed_at !== undefined}
				<dt>Password last changed</dt>
				<dd>{formatTime(account.password_changed_at)}</dd>
			{/if}
		</dl>

		{#key passwordChanges}
			<Sessions />
		{/key}
		<ChangePassword
			username={account.username}
			onChanged={() => {
				passwordChanges++;
				void load(); // the password's date changed
			}}
		/>
		<NewRecoveryCodes
			username={account.username}
			left={account.recovery_codes_left}
			onReplaced={() => {
				void load();
			}}
		/>
	{:else if error === ''}
		<p class="muted" aria-live="polite">Loading…</p>
	{/if}
	{#if error !== ''}
		<p class="error" role="alert">{error}</p>
	{/if}
</div>

<style>
	.account {
		max-width: 40rem;
		margin: 0 auto;
	}

	.account :global(section) {
		margin-top: var(--space-4);
	}

	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: var(--space-1) var(--space-3);
	}

	dt {
		color: var(--color-muted);
	}

	dd {
		margin: 0;
	}

	.muted {
		color: var(--color-muted);
	}

	.error {
		color: var(--color-accent);
	}
</style>
