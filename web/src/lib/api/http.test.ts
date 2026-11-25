import { afterEach, describe, expect, it, vi } from 'vitest';

import { apiFetch } from './http';

interface Got {
	data: unknown;
	status: number;
	headers: Headers;
}

function stubFetch(res: Response): ReturnType<typeof vi.fn> {
	const f = vi.fn(() => Promise.resolve(res));
	vi.stubGlobal('fetch', f);
	return f;
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('apiFetch', () => {
	it('decodes a JSON body with the status and headers', async () => {
		stubFetch(Response.json({ version: 'v1' }, { status: 200, headers: { 'X-Request-Id': 'r1' } }));
		const got = await apiFetch<Got>('/api/v1/version', { method: 'GET' });
		expect(got.status).toBe(200);
		expect(got.data).toEqual({ version: 'v1' });
		expect(got.headers.get('X-Request-Id')).toBe('r1');
	});

	it('passes the URL and init through', async () => {
		const f = stubFetch(new Response(null, { status: 204 }));
		await apiFetch<Got>('/api/v1/auth/logout', { method: 'POST' });
		expect(f).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' });
	});

	it.each([204, 205, 304])('has no data for a %i', async (status) => {
		stubFetch(new Response(null, { status }));
		const got = await apiFetch<Got>('/x', {});
		expect(got.status).toBe(status);
		expect(got.data).toBeUndefined();
	});

	it('decodes a problem body on an error status', async () => {
		stubFetch(Response.json({ type: 'about:blank', status: 404 }, { status: 404 }));
		const got = await apiFetch<Got>('/x', {});
		expect(got.status).toBe(404);
		expect(got.data).toEqual({ type: 'about:blank', status: 404 });
	});

	it('rejects a body that is not JSON', async () => {
		stubFetch(new Response('<html>', { status: 502 }));
		await expect(apiFetch<Got>('/x', {})).rejects.toThrow(SyntaxError);
	});
});
