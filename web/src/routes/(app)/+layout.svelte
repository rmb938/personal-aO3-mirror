<!--
	Every page that needs a session (all but the sign-in pages). It checks the session once, shows
	the pages with a header once signed in, and sends the browser to the login page, remembering
	where it was, whenever the session turns out to be gone (docs/part4-ui.md stage 5b).
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import type { Snippet } from 'svelte';

	import { type Account, getAccount, logout } from '#lib/api/api.gen.ts';
	import { onSessionLost } from '#lib/api/http.ts';
	import { describeFailure, unreachable } from '#lib/auth/messages.ts';
	import { loginHref } from '#lib/auth/next.ts';

	let { children }: { children: Snippet } = $props();

	type Status =
		| { kind: 'checking' }
		| { kind: 'signed-in'; account: Account }
		| { kind: 'leaving' }
		| { kind: 'failed'; text: string };

	let status: Status = $state({ kind: 'checking' });
	let loggingOut = $state(false);
	// Set once the session is known to be gone; the login page is loading.
	let leaving = false;

	function toLogin(): void {
		leaving = true;
		status = { kind: 'leaving' };
		// loginHref only builds paths on this origin.
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		void goto(loginHref(page.url), { replace: true });
	}

	async function check(): Promise<void> {
		status = { kind: 'checking' };
		try {
			const res = await getAccount();
			if (res.status === 200) {
				status = { kind: 'signed-in', account: res.data };
			} else if (res.status === 401 && !leaving) {
				// Not signed in, but the refresh failed for another reason (a rate limit, a server
				// error), so the session may still be there: let the user retry.
				status = { kind: 'failed', text: "Your session couldn't be refreshed. Try again in a moment." };
			} else if (res.status !== 401) {
				status = { kind: 'failed', text: describeFailure(res, '').text };
			}
			// Otherwise apiFetch fired onSessionLost, and toLogin is on its way to the login page.
		} catch {
			status = { kind: 'failed', text: unreachable };
		}
	}

	async function signOut(): Promise<void> {
		loggingOut = true;
		try {
			// 204, or a 401 for a session already gone (which fires onSessionLost too): either
			// way it is over here.
			const res = await logout();
			if (res.status === 204 || res.status === 401) {
				leaving = true;
				status = { kind: 'leaving' };
				await goto(resolve('/login'), { replace: true });
				return;
			}
			status = { kind: 'failed', text: describeFailure(res, '').text };
		} catch {
			status = { kind: 'failed', text: unreachable };
		} finally {
			loggingOut = false;
		}
	}

	$effect(() => onSessionLost(toLogin));
	$effect(() => {
		void check();
	});
</script>

{#if status.kind === 'signed-in'}
	<header>
		<!-- The home page's route ID: its group, (app), is not part of the path ("/"). -->
		<a class="home" href={resolve('/(app)')}>ao3mirror</a>
		<span class="account">
			Signed in as <strong>{status.account.username}</strong>
			<button type="button" onclick={signOut} disabled={loggingOut}>Log out</button>
		</span>
	</header>
	{@render children()}
{:else if status.kind === 'failed'}
	<p role="alert">{status.text}</p>
	<button type="button" onclick={check}>Try again</button>
{:else}
	<p class="muted" aria-live="polite">Checking your session…</p>
{/if}

<style>
	header {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: baseline;
		justify-content: space-between;
		padding-bottom: var(--space-2);
		margin-bottom: var(--space-3);
		border-bottom: 1px solid var(--color-muted);
	}

	.home {
		color: var(--color-accent);
		font-weight: bold;
		text-decoration: none;
	}

	.account {
		display: flex;
		gap: var(--space-2);
		align-items: baseline;
	}

	.muted {
		color: var(--color-muted);
	}
</style>
