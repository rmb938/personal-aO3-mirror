// Helpers for the signup and reset pages (docs/part4-ui.md stage 6b): reading the link's
// fragment, and what the new TOTP secret's otpauth URI holds.

import { ResetScope } from '#lib/api/api.gen.ts';

/** An invite or reset link's fragment, read: the token and, on a reset link, its scope. */
export interface Link {
	token: string;
	scope?: ResetScope;
}

/** The InviteToken schema's pattern. */
const tokenPattern = /^inv_[A-Za-z0-9_-]{43}$/;

function isScope(s: string): s is ResetScope {
	return (Object.values(ResetScope) as string[]).includes(s);
}

/**
 * Reads `#token=…` (and for a reset link `&scope=…`) from a location hash. Returns undefined
 * when the token is missing or malformed, or a reset link's scope is: the link was cut short
 * when it was copied, so there is nothing to send.
 */
export function readLink(hash: string, kind: 'signup' | 'reset'): Link | undefined {
	const params = new URLSearchParams(hash.replace(/^#/, ''));
	const token = params.get('token');
	if (token === null || !tokenPattern.test(token)) {
		return undefined;
	}
	if (kind === 'signup') {
		return { token };
	}
	const scope = params.get('scope');
	if (scope === null || !isScope(scope)) {
		return undefined;
	}
	return { token, scope };
}

/** Whether a reset of this scope asks for a new password. */
export function scopeHasPassword(scope: ResetScope): boolean {
	return scope === 'password' || scope === 'both';
}

/**
 * The username in an otpauth URI's label ("issuer:username", the Enrollment schema's pattern),
 * or undefined. A reset page learns the account's name only from here.
 */
export function otpauthUsername(uri: string): string | undefined {
	const m = /^otpauth:\/\/totp\/[^:?]+:([a-z0-9_-]{3,32})\?/.exec(uri);
	return m?.[1];
}

/** A base32 secret in groups of four, easier to type by hand ("ABCD EFGH …"). */
export function groupSecret(secret: string): string {
	return (secret.match(/.{1,4}/g) ?? []).join(' ');
}
