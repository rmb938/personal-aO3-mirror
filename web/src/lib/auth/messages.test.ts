import { describe, expect, it } from 'vitest';

import {
	describeFailure,
	invalidPointers,
	passwordRefused,
	retryAfterSeconds,
	revealDisabled,
	usernameRefused,
	usernameTaken
} from './messages';

const base = 'https://github.com/rmb938/personal-aO3-mirror/blob/main/docs/api/problems.md#';

function failure(status: number, slug: string | undefined, headers: Record<string, string> = {}) {
	return {
		status,
		data: slug === undefined ? undefined : { type: base + slug, title: 'x', status },
		headers: new Headers(headers)
	};
}

describe('describeFailure', () => {
	it("uses the step's own text for invalid credentials", () => {
		expect(describeFailure(failure(401, 'invalid-credentials'), 'wrong password')).toEqual({
			text: 'wrong password'
		});
	});

	it('says how long a rate limit or backoff lasts', () => {
		expect(describeFailure(failure(429, 'rate-limited', { 'Retry-After': '1' }), '')).toEqual({
			text: 'Too many tries. Try again in 1 second.',
			retryAfter: 1
		});
		expect(describeFailure(failure(429, 'rate-limited', { 'Retry-After': '30' }), '')).toEqual({
			text: 'Too many tries. Try again in 30 seconds.',
			retryAfter: 30
		});
	});

	it('handles a rate limit without a usable Retry-After', () => {
		const msg = describeFailure(failure(429, 'rate-limited', { 'Retry-After': 'soon' }), '');
		expect(msg.retryAfter).toBeUndefined();
		expect(msg.text).toMatch(/Too many tries/);
	});

	it('explains a server that refuses reveal', () => {
		expect(describeFailure(failure(403, 'reveal-disabled'), '').text).toBe(revealDisabled);
	});

	it('never shows the problem detail', () => {
		const res = failure(500, 'internal-error');
		(res.data as Record<string, unknown>)['detail'] = 'DETAIL-SENTINEL';
		const msg = describeFailure(res, '');
		expect(msg.text).toBe('Something went wrong (HTTP 500). Try again.');
	});

	it("uses the step's own text for forbidden, else calls it cross-site", () => {
		expect(describeFailure(failure(403, 'forbidden'), { forbidden: 'wrong current password' }).text).toBe(
			'wrong current password'
		);
		expect(describeFailure(failure(403, 'forbidden'), 'wrong password').text).toMatch(/cross-site/);
	});

	it('explains the password policy when the new password is refused', () => {
		const res = failure(422, 'validation-error');
		(res.data as Record<string, unknown>)['errors'] = [{ detail: 'DETAIL-SENTINEL', pointer: '/password' }];
		expect(describeFailure(res, '').text).toBe(passwordRefused);

		(res.data as Record<string, unknown>)['errors'] = [{ detail: 'DETAIL-SENTINEL', pointer: '/code' }];
		expect(describeFailure(res, '').text).toBe('Check what you entered: it is not in the expected form.');
	});

	it('explains the username rule when the new username is refused', () => {
		const res = failure(422, 'validation-error');
		(res.data as Record<string, unknown>)['errors'] = [{ detail: 'DETAIL-SENTINEL', pointer: '/username' }];
		expect(describeFailure(res, '').text).toBe(usernameRefused);
	});

	it('says a taken username is taken', () => {
		expect(describeFailure(failure(409, 'already-exists'), '').text).toBe(usernameTaken);
	});

	it('falls back on the status when the body is no problem', () => {
		expect(describeFailure(failure(502, undefined), '').text).toBe(
			'Something went wrong (HTTP 502). Try again.'
		);
	});
});

describe('invalidPointers', () => {
	it('lists the pointers and skips everything else', () => {
		expect(
			invalidPointers({ errors: [{ pointer: '/password' }, { parameter: 'page' }, 'junk', null, { pointer: 3 }] })
		).toEqual(['/password']);
		expect(invalidPointers({ errors: 'no' })).toEqual([]);
		expect(invalidPointers(undefined)).toEqual([]);
	});
});

describe('retryAfterSeconds', () => {
	it.each([
		['5', 5],
		[' 12 ', 12],
		['0', 1],
		['-3', undefined],
		['1.5', undefined],
		['Wed, 21 Oct 2015 07:28:00 GMT', undefined]
	])('reads %j as %j', (raw, want) => {
		expect(retryAfterSeconds(new Headers({ 'Retry-After': raw }))).toBe(want);
	});

	it('is undefined without the header', () => {
		expect(retryAfterSeconds(new Headers())).toBeUndefined();
	});
});
