import { describe, expect, it } from 'vitest';

import { groupSecret, otpauthUsername, readLink, scopeHasPassword } from './enroll';

// A token with the InviteToken schema's shape (synthetic).
const token = 'inv_' + 'A'.repeat(42) + 'w';

describe('readLink', () => {
	it('reads a signup link', () => {
		expect(readLink(`#token=${token}`, 'signup')).toEqual({ token });
	});

	it('reads a reset link with its scope', () => {
		for (const scope of ['password', 'totp', 'both'] as const) {
			expect(readLink(`#token=${token}&scope=${scope}`, 'reset')).toEqual({ token, scope });
		}
	});

	it('ignores a scope on a signup link', () => {
		expect(readLink(`#token=${token}&scope=both`, 'signup')).toEqual({ token });
	});

	it('refuses a link cut short or mangled', () => {
		for (const hash of [
			'',
			'#',
			'#token=',
			`#token=${token.slice(0, -1)}`,
			`#token=${token}x`,
			`#token=enr_${token.slice(4)}`,
			`#scope=both`
		]) {
			expect(readLink(hash, 'signup'), hash).toBeUndefined();
		}
		for (const hash of [`#token=${token}`, `#token=${token}&scope=`, `#token=${token}&scope=all`]) {
			expect(readLink(hash, 'reset'), hash).toBeUndefined();
		}
	});
});

describe('scopeHasPassword', () => {
	it('is true when the reset replaces the password', () => {
		expect(scopeHasPassword('password')).toBe(true);
		expect(scopeHasPassword('both')).toBe(true);
		expect(scopeHasPassword('totp')).toBe(false);
	});
});

describe('otpauthUsername', () => {
	const secret = 'A'.repeat(32);
	it('reads the label after the issuer', () => {
		expect(
			otpauthUsername(
				`otpauth://totp/ao3mirror:fixture_user?secret=${secret}&issuer=ao3mirror&algorithm=SHA1&digits=6&period=30`
			)
		).toBe('fixture_user');
		expect(otpauthUsername(`otpauth://totp/My%20Mirror:abc?secret=${secret}&issuer=My%20Mirror`)).toBe('abc');
	});

	it('is undefined for anything else', () => {
		expect(otpauthUsername('otpauth://totp/fixture_user?secret=x')).toBeUndefined();
		expect(otpauthUsername('https://example.test/a:b?c')).toBeUndefined();
	});
});

describe('groupSecret', () => {
	it('splits into groups of four', () => {
		expect(groupSecret('ABCDEFGHIJ')).toBe('ABCD EFGH IJ');
		expect(groupSecret('')).toBe('');
	});
});
