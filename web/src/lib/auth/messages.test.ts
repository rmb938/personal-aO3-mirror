import { describe, expect, it } from 'vitest';

import { describeFailure, retryAfterSeconds, revealDisabled } from './messages';

const base = 'https://github.com/rmb938/personal-aO3-mirror/blob/main/docs/api/problems.md#';

function failure(status: number, slug: string | undefined, headers: Record<string, string> = {}) {
	return {
		status,
		data: slug === undefined ? undefined : { type: base + slug, title: 'x', status },
		headers: new Headers(headers)
	};
}

describe('describeFailure', () => {
	it('uses the step’s own text for invalid credentials', () => {
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

	it('falls back on the status when the body is no problem', () => {
		expect(describeFailure(failure(502, undefined), '').text).toBe(
			'Something went wrong (HTTP 502). Try again.'
		);
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
