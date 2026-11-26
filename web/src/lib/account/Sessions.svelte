<!--
	The account's signed-in sessions, newest first, each but this one with a button to end it
	(docs/part4-ui.md stage 6). This session is ended with "Log out" in the header instead, which
	also clears its cookies.
-->
<script lang="ts">
	import { listAccountSessions, revokeAccountSession, type Session } from '#lib/api/api.gen.ts';
	import { describeFailure, unreachable } from '#lib/auth/messages.ts';
	import { formatTime } from '#lib/format.ts';

	let sessions: Session[] | undefined = $state();
	let error = $state('');
	let ending: number | undefined = $state();

	async function load(): Promise<void> {
		try {
			const res = await listAccountSessions();
			if (res.status === 200) {
				sessions = res.data.items;
				error = '';
			} else if (res.status !== 401) {
				// A 401 means the session is gone, and the layout is on its way to the login page.
				error = describeFailure(res, '').text;
			}
		} catch {
			error = unreachable;
		}
	}

	async function end(id: number): Promise<void> {
		ending = id;
		error = '';
		try {
			const res = await revokeAccountSession(id);
			// 404: it ended meanwhile (it expired, or another device ended it). Either way it's gone.
			if (res.status === 204 || res.status === 404) {
				await load();
			} else if (res.status !== 401) {
				error = describeFailure(res, '').text;
			}
		} catch {
			error = unreachable;
		} finally {
			ending = undefined;
		}
	}

	$effect(() => {
		void load();
	});
</script>

<section aria-labelledby="sessions-heading">
	<h2 id="sessions-heading">Where you're signed in</h2>
	{#if sessions === undefined && error === ''}
		<p class="muted" aria-live="polite">Loading…</p>
	{:else if sessions !== undefined}
		<ul>
			{#each sessions as s (s.id)}
				<li aria-label={s.current ? 'This device' : `Session ${String(s.id)}`}>
					<div class="when">
						{#if s.current}
							<strong>This device</strong>
						{/if}
						<span>Signed in {formatTime(s.created_at)}</span>
						<span class="muted">Last active {formatTime(s.last_used_at)} · ends {formatTime(s.expires_at)}</span>
					</div>
					{#if !s.current}
						<button
							type="button"
							disabled={ending !== undefined}
							onclick={() => {
								void end(s.id);
							}}
						>
							{ending === s.id ? 'Signing out…' : 'Sign out'}
						</button>
					{/if}
				</li>
			{/each}
		</ul>
		<p class="muted">To sign out this device, use Log out at the top.</p>
	{/if}
	{#if error !== ''}
		<p class="error" role="alert">{error}</p>
	{/if}
</section>

<style>
	ul {
		padding: 0;
		list-style: none;
	}

	li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		justify-content: space-between;
		padding: var(--space-2) 0;
		border-bottom: 1px solid var(--color-muted);
	}

	.when {
		display: flex;
		flex-direction: column;
	}

	button {
		font: inherit;
		padding: var(--space-1) var(--space-2);
	}

	.muted {
		color: var(--color-muted);
		font-size: 0.875rem;
	}

	.error {
		color: var(--color-accent);
	}
</style>
