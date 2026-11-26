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

/** The password policy (the spec's Password schema, auth.CheckPassword), to show beside a new password. */
export const passwordPolicy =
	"Use 8 to 128 characters. It can't be your username or a common password, and there are no other rules.";

/** Shown when the server refuses a new password (a 422 at /password). */
export const passwordRefused = `Choose a different password. ${passwordPolicy}`;

/** The username rule (the spec's SignupRequest.username), to show beside a new username. */
export const usernameRule = 'Use 3 to 32 letters, digits, _ or -. Letters are stored in lower case.';

/** Shown when the server refuses a new username's form (a 422 at /username). */
export const usernameRefused = `Choose a different username. ${usernameRule}`;

/** Shown for already-exists at signup: someone has the username. */
export const usernameTaken = 'That username is taken. Choose another one.';

/** Per-step texts for the problems whose meaning depends on the step. */
export interface StepTexts {
	/** invalid-credentials: a wrong password or code at a sign-in step. */
	invalid?: string;
	/**
	 * forbidden: on a signed-in change (password, recovery codes), a wrong current password or
	 * code. Elsewhere it is a cross-site refusal.
	 */
	forbidden?: string;
}

/**
 * The message for a failed auth step. `texts` (or a string, for invalid-credentials alone)
 * gives the texts for the problems that mean something different at each step.
 */
export function describeFailure(res: Failure, texts: string | StepTexts): Message {
	const { invalid, forbidden } = typeof texts === 'string' ? { invalid: texts } : texts;
	switch (problemSlug(res.data)) {
		case 'invalid-credentials':
			return { text: invalid ?? "That didn't work. Check what you entered and try again." };
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
			return {
				text: forbidden ?? 'The server refused this as a cross-site request. Reload the page and try again.'
			};
		case 'already-exists':
			// Only signup answers with it among the auth steps.
			return { text: usernameTaken };
		case 'validation-error': {
			const pointers = invalidPointers(res.data);
			if (pointers.includes('/password')) {
				return { text: passwordRefused };
			}
			if (pointers.includes('/username')) {
				return { text: usernameRefused };
			}
			return { text: 'Check what you entered: it is not in the expected form.' };
		}
		default:
			return { text: `Something went wrong (HTTP ${String(res.status)}). Try again.` };
	}
}

/** The body members a validation-error names (its errors' JSON Pointers), never their details. */
export function invalidPointers(data: unknown): string[] {
	if (typeof data !== 'object' || data === null || !('errors' in data) || !Array.isArray(data.errors)) {
		return [];
	}
	return data.errors.flatMap((e: unknown) =>
		typeof e === 'object' && e !== null && 'pointer' in e && typeof e.pointer === 'string' ? [e.pointer] : []
	);
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
