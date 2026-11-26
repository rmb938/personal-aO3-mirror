import { describe, expect, it } from 'vitest';

import { loginHref, safeNext } from './next';

const origin = 'https://mirror.example';

describe('loginHref', () => {
	it('leaves out the home page', () => {
		expect(loginHref(new URL('/', origin))).toBe('/login');
	});

	it('remembers the path, query and fragment', () => {
		expect(loginHref(new URL('/works/1001?view=full#chapter-2', origin))).toBe(
			'/login?next=%2Fworks%2F1001%3Fview%3Dfull%23chapter-2'
		);
	});

	it('round-trips through safeNext', () => {
		const href = new URL(loginHref(new URL('/works/1001?view=full#c2', origin)), origin);
		expect(safeNext(href.searchParams.get('next'), origin)).toBe('/works/1001?view=full#c2');
	});
});

describe('safeNext', () => {
	it.each([
		['/works/1001', '/works/1001'],
		['/works/1001?view=full#c2', '/works/1001?view=full#c2'],
		['/a/../account', '/account']
	])('follows the app path %j', (raw, want) => {
		expect(safeNext(raw, origin)).toBe(want);
	});

	it.each([
		null,
		'',
		'works/1001',
		'//evil.example/x',
		'/\\evil.example/x',
		'https://evil.example/',
		'javascript:alert(1)',
		'/login',
		'/login?next=/x',
		'/api',
		'/api/v1/auth/logout'
	])('sends %j home', (raw) => {
		expect(safeNext(raw, origin)).toBe('/');
	});
});
