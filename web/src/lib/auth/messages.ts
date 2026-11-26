// What the sign-in pages tell the user when the API says no. Problems are told apart by their
// type's slug, never the detail text (docs/part4-ui.md, "Auth in the browser").

import { problemSlug } from '#lib/api/problem.ts';

/** A failed response, as the generated client returns it. */
export interface Failure {
	status: number;
	data: unknown;
	headers: Headers;
}

/** What to show for a failure, and for a rate limit how many seconds to wait first. */
export interface Message {
	text: string;
	retryAfter?: number;
}

/** Shown when the server can't be reached at all (fetch threw). */
export const unreachable = "Couldn't reach the server. Check your connection and try again.";

/** Shown for reveal-disabled: the server runs with --no-reveal, so the UI has nothing to show. */
export const revealDisabled =
	'This server runs with --no-reveal, so it never shows your mirror. ' +
	'Restart it without --no-reveal to use the web UI.';

/**
 * The message for a failed auth step. `invalid` is the text for invalid-credentials, which
 * means something different at each step (a wrong password, a wrong code).
 */
export function describeFailure(res: Failure, invalid: string): Message {
	switch (problemSlug(res.data)) {
		case 'invalid-credentials':
			return { text: invalid };
		case 'rate-limited': {
			const wait = retryAfterSeconds(res.headers);
			if (wait === undefined) {
				return { text: 'Too many tries. Wait a little and try again.' };
			}
			return { text: `Too many tries. Try again in ${plural(wait, 'second')}.`, retryAfter: wait };
		}
		case 'reveal-disabled':
			return { text: revealDisabled };
		case 'unavailable':
			return { text: 'The server is busy. Try again in a moment.' };
		case 'forbidden':
			return { text: 'The server refused this as a cross-site request. Reload the page and try again.' };
		case 'validation-error':
			return { text: 'Check what you entered: it is not in the expected form.' };
		default:
			return { text: `Something went wrong (HTTP ${String(res.status)}). Try again.` };
	}
}

/** Retry-After as a whole number of seconds (the API only sends delay-seconds), at least 1. */
export function retryAfterSeconds(headers: Headers): number | undefined {
	const raw = headers.get('Retry-After');
	if (raw === null || !/^\d+$/.test(raw.trim())) {
		return undefined;
	}
	return Math.max(1, Number(raw.trim()));
}

/** "1 second", "5 seconds". */
export function plural(n: number, unit: string): string {
	return `${String(n)} ${unit}${n === 1 ? '' : 's'}`;
}
