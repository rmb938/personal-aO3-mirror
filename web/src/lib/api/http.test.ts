import { afterEach, describe, expect, it, vi } from 'vitest';

import { REVEAL_PARAM, REVEAL_VALUE } from '#lib/reveal.gen.ts';

import { getRefreshUrl } from './api.gen';
import { apiFetch, csrfHeader, onSessionLost, refreshLockName, refreshPath } from './http';
import { problemSlug } from './problem';

interface Got {
	data: unknown;
	status: number;
	headers: Headers;
}

interface Call {
	path: string;
	query: URLSearchParams;
	method: string;
	headers: Headers;
	body: unknown;
}

const reveal = `${REVEAL_PARAM}=${REVEAL_VALUE}`;

function problem(slug: string, status: number): Response {
	return Response.json(
		{ type: `https://example.invalid/problems.md#${slug}`, title: slug, status },
		{ status, headers: { 'Content-Type': 'application/problem+json' } }
	);
}

// A fake API with one cookie session, as the browser sees it: a request is signed in while
// `signedIn` is true, and a refresh answers `refreshStatus` (and signs in on a 200).
class FakeAPI {
	signedIn = true;
	refreshStatus = 200;
	readonly calls: Call[] = [];

	constructor(private readonly answer: (call: Call) => Response = () => Response.json({ ok: true })) {
		vi.stubGlobal('fetch', vi.fn(this.handle.bind(this)));
	}

	/** The calls to paths other than the refresh route. */
	get requests(): Call[] {
		return this.calls.filter((c) => c.path !== refreshPath);
	}

	get refreshes(): Call[] {
		return this.calls.filter((c) => c.path === refreshPath);
	}

	private handle(url: string, init: RequestInit): Promise<Response> {
		const [path = '', query = ''] = url.split('?');
		const call: Call = {
			path,
			query: new URLSearchParams(query),
			method: init.method ?? 'GET',
			headers: new Headers(init.headers),
			body: typeof init.body === 'string' ? JSON.parse(init.body) : undefined
		};
		this.calls.push(call);
		if (path === refreshPath) {
			if (this.refreshStatus !== 200) {
				return Promise.resolve(
					problem(this.refreshStatus === 401 ? 'invalid-credentials' : 'rate-limited', this.refreshStatus)
				);
			}
			this.signedIn = true;
			return Promise.resolve(
				Response.json({ transport: 'cookie', expires_in: 900, refresh_expires_at: '2026-12-01T00:00:00Z' })
			);
		}
		if (!this.signedIn) {
			return Promise.resolve(problem('unauthorized', 401));
		}
		return Promise.resolve(this.answer(call));
	}
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('apiFetch', () => {
	it('decodes a JSON body with the status and headers', async () => {
		new FakeAPI(() => Response.json({ version: 'v1' }, { headers: { 'X-Request-Id': 'r1' } }));
		const got = await apiFetch<Got>('/api/v1/version', { method: 'GET' });
		expect(got.status).toBe(200);
		expect(got.data).toEqual({ version: 'v1' });
		expect(got.headers.get('X-Request-Id')).toBe('r1');
	});

	it.each([204, 205, 304])('has no data for a %i', async (status) => {
		new FakeAPI(() => new Response(null, { status }));
		const got = await apiFetch<Got>('/x', {});
		expect(got.status).toBe(status);
		expect(got.data).toBeUndefined();
	});

	it('decodes a problem body on an error status', async () => {
		new FakeAPI(() => problem('not-found', 404));
		const got = await apiFetch<Got>('/x', {});
		expect(got.status).toBe(404);
		expect(problemSlug(got.data)).toBe('not-found');
	});

	it('rejects a body that is not JSON', async () => {
		new FakeAPI(() => new Response('<html>', { status: 502 }));
		await expect(apiFetch<Got>('/x', {})).rejects.toThrow(SyntaxError);
	});
});

describe('the reveal param', () => {
	it.each([
		['/api/v1/works/1', ''],
		['/api/v1/works?page_size=5&sort=title', 'page_size=5&sort=title&']
	])('is added to %s', async (url, rest) => {
		const api = new FakeAPI();
		await apiFetch<Got>(url, { method: 'GET' });
		expect(fetch).toHaveBeenCalledWith(`${url.split('?')[0] ?? ''}?${rest}${reveal}`, expect.anything());
		expect(api.calls[0]?.query.getAll(REVEAL_PARAM)).toEqual([REVEAL_VALUE]);
	});
});

describe('the CSRF header', () => {
	it.each(['POST', 'PUT', 'PATCH', 'DELETE', 'QUERY', 'post'])('is sent on %s', async (method) => {
		const api = new FakeAPI();
		await apiFetch<Got>('/x', { method, headers: { 'Content-Type': 'application/json' }, body: '{}' });
		expect(api.calls[0]?.headers.get(csrfHeader)).toBe('1');
		expect(api.calls[0]?.headers.get('Content-Type')).toBe('application/json');
	});

	it.each(['GET', 'HEAD', undefined])('is not sent on %s', async (method) => {
		const api = new FakeAPI();
		await apiFetch<Got>('/x', method === undefined ? {} : { method });
		expect(api.calls[0]?.headers.has(csrfHeader)).toBe(false);
	});

	it('keeps the other init members', async () => {
		new FakeAPI();
		const signal = new AbortController().signal;
		await apiFetch<Got>('/x', { method: 'POST', body: '{"a":1}', signal });
		expect(fetch).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ body: '{"a":1}', signal }));
	});
});

describe('session refresh', () => {
	it('uses the generated refresh route', () => {
		expect(refreshPath).toBe(getRefreshUrl());
	});

	it('refreshes with the cookie transport and retries the request', async () => {
		const api = new FakeAPI();
		api.signedIn = false;
		const got = await apiFetch<Got>('/api/v1/account/password', {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: '{"code":"123456"}'
		});
		expect(got.status).toBe(200);
		expect(api.refreshes).toHaveLength(1);
		const r = api.refreshes[0];
		expect(r?.method).toBe('POST');
		expect(r?.body).toEqual({ transport: 'cookie' });
		expect(r?.headers.get(csrfHeader)).toBe('1');
		expect(r?.headers.get('Content-Type')).toBe('application/json');
		expect(r?.query.get(REVEAL_PARAM)).toBe(REVEAL_VALUE);
		// The first try, the try after taking the lock, and the one after the refresh, each
		// with the same body.
		expect(api.requests.map((c) => c.body)).toEqual([{ code: '123456' }, { code: '123456' }, { code: '123456' }]);
	});

	it('does not refresh on other 401s', async () => {
		const api = new FakeAPI(() => problem('invalid-credentials', 401));
		const got = await apiFetch<Got>('/api/v1/auth/login', { method: 'POST', body: '{}' });
		expect(got.status).toBe(401);
		expect(api.refreshes).toHaveLength(0);
	});

	it('reports the session lost when the refresh is refused', async () => {
		const api = new FakeAPI();
		api.signedIn = false;
		api.refreshStatus = 401;
		const lost = vi.fn();
		const off = onSessionLost(lost);
		const got = await apiFetch<Got>('/x', {});
		off();
		expect(got.status).toBe(401);
		expect(problemSlug(got.data)).toBe('unauthorized');
		expect(lost).toHaveBeenCalledOnce();
	});

	it('does not report the session lost when the refresh fails otherwise', async () => {
		const api = new FakeAPI();
		api.signedIn = false;
		api.refreshStatus = 429;
		const lost = vi.fn();
		const off = onSessionLost(lost);
		const got = await apiFetch<Got>('/x', {});
		off();
		expect(got.status).toBe(401);
		expect(lost).not.toHaveBeenCalled();
	});

	it('stops calling a listener once it unsubscribes', async () => {
		const api = new FakeAPI();
		api.signedIn = false;
		api.refreshStatus = 401;
		const lost = vi.fn();
		onSessionLost(lost)();
		await apiFetch<Got>('/x', {});
		expect(lost).not.toHaveBeenCalled();
	});

	it('refreshes once for requests that fail together', async () => {
		const api = new FakeAPI();
		api.signedIn = false;
		const got = await Promise.all([apiFetch<Got>('/a', {}), apiFetch<Got>('/b', {}), apiFetch<Got>('/c', {})]);
		expect(got.map((g) => g.status)).toEqual([200, 200, 200]);
		expect(api.refreshes).toHaveLength(1);
	});

	it('does not refresh when another tab did while it waited', async () => {
		const api = new FakeAPI();
		api.signedIn = false;
		// Another tab holds the lock and refreshes. Web Locks are per origin, so a second
		// holder in this process stands in for it.
		let release = (): void => undefined;
		const otherTab = navigator.locks.request(
			refreshLockName,
			() =>
				new Promise<void>((resolve) => {
					release = () => {
						api.signedIn = true;
						resolve();
					};
				})
		);
		const pending = apiFetch<Got>('/x', {});
		await vi.waitFor(() => {
			expect(api.requests).toHaveLength(1);
		});
		release();
		await otherTab;
		const got = await pending;
		expect(got.status).toBe(200);
		expect(api.refreshes).toHaveLength(0);
	});
});
