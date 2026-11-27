<!--
	A work's pages (docs/part4-ui.md stage 8): this layout loads the work once and hands it to the
	view below it (the entire work, or one chapter), so moving between chapters fetches only the
	chapter. It loads the work's skin with it and applies it while it is shown (stage 8c).
-->
<script lang="ts">
	import { getWork, getWorkSkin, type Work } from '#lib/api/api.gen.ts';
	import { describeFailure, unreachable } from '#lib/auth/messages.ts';
	import { imageMap } from '#lib/sanitize/html.ts';
	import { sanitizeSkin } from '#lib/sanitize/skin.ts';
	import { type Skin, setWorkContext } from '#lib/work/context.ts';

	import type { LayoutProps } from './$types';

	// The `id` matcher (src/params.ts) hands over the work ID as a number.
	let { params, children }: LayoutProps = $props();

	/** Shown for a 404: the route matched an ID, but the mirror has no such work. */
	const notMirrored = "This work isn't in your mirror.";

	// The ID alone, so moving to another chapter of the same work doesn't load it again.
	const id = $derived(params.id);
	let work: Work | undefined = $state();
	let error = $state('');
	const images = $derived(work === undefined ? new Map<string, string>() : imageMap(work.images));
	let skin: Skin = $state({ kind: 'none' });
	let skinShown = $state(true);

	setWorkContext({
		get work(): Work {
			if (work === undefined) {
				throw new Error('the work view rendered before its work loaded');
			}
			return work;
		},
		get images() {
			return images;
		},
		get skin() {
			return skin;
		},
		get skinShown() {
			return skinShown;
		},
		set skinShown(shown: boolean) {
			skinShown = shown;
		}
	});

	/** The work's skin, sanitized. Failing to load it shows a note (a 401 is on its way to login). */
	async function loadSkin(want: number): Promise<Skin> {
		try {
			const res = await getWorkSkin(want);
			if (res.status !== 200) {
				return res.status === 404 || res.status === 401 ? { kind: 'none' } : { kind: 'failed' };
			}
			const sheet = res.data.css === undefined ? undefined : sanitizeSkin(res.data.css);
			return sheet === undefined ? { kind: 'none' } : { kind: 'sheet', sheet };
		} catch {
			return { kind: 'failed' };
		}
	}

	async function load(want: number): Promise<void> {
		work = undefined;
		error = '';
		skin = { kind: 'none' };
		skinShown = true;
		try {
			const res = await getWork(want);
			if (want !== id) {
				return; // the user has gone on to another work
			}
			if (res.status === 200) {
				// The skin once the work is known to be there (so a missing work is one 404), and
				// before the text shows, so it never shows unstyled first.
				const loadedSkin = await loadSkin(want);
				if (want !== id) {
					return;
				}
				skin = loadedSkin;
				work = res.data;
			} else if (res.status === 404) {
				error = notMirrored;
			} else if (res.status !== 401) {
				// A 401 fired onSessionLost: the layout is on its way to the login page.
				error = describeFailure(res, '').text;
			}
		} catch {
			error = unreachable;
		}
	}

	$effect(() => {
		void load(id);
	});

	// The skin applies through adoptedStyleSheets (no <style>, so the CSP needs no inline
	// styles), only while the work is shown and its style isn't hidden. Its rules are inside
	// @scope (.ao3m-skin-scope), so they reach only WorkReader's #workskin.
	$effect(() => {
		if (skin.kind !== 'sheet' || !skinShown || work === undefined) {
			return;
		}
		const sheet = skin.sheet;
		document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet];
		return () => {
			document.adoptedStyleSheets = document.adoptedStyleSheets.filter((s) => s !== sheet);
		};
	});
</script>

<svelte:head>
	<title>{work === undefined ? 'Work' : work.title} · ao3mirror</title>
</svelte:head>

<article class="work">
	{#if work !== undefined}
		{@render children()}
	{:else if error === ''}
		<p class="muted" aria-live="polite">Loading…</p>
	{:else}
		<p class="error" role="alert">{error}</p>
	{/if}
</article>

<style>
	.work {
		max-width: var(--reading-width);
		margin: 0 auto;
	}

	.muted {
		color: var(--color-muted);
	}

	.error {
		color: var(--color-accent);
	}
</style>
