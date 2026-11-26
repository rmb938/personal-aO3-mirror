// The one function every generated API call goes through (orval.config.ts). It returns what
// the generated response types describe: the decoded body, the status and the headers. On the
// way it adds the reveal param and the CSRF header, and when the API says the request isn't
// signed in it refreshes the session and retries, once at a time across all of this origin's
// tabs (docs/part4-ui.md, "Auth in the browser").

import { REVEAL_PARAM, REVEAL_VALUE } from '#lib/reveal.gen.ts';

import { problemSlug } from './problem';

/** Every request but GET and HEAD sends this header with the value 1 (the API's CSRF check). */
export const csrfHeader = 'X-Ao3mirror-Csrf';

/**
 * The refresh route. It is the generated getRefreshUrl(), which this file can't import:
 * api.gen.ts imports apiFetch from here. A test keeps the two equal.
 */
export const refreshPath = '/api/v1/auth/refresh';

/**
 * The Web Lock every tab of this origin holds around a refresh. The tabs share the session
 * cookies, and the API ends the session when one refresh cookie is traded twice, so two tabs
 * must never refresh at the same time.
 */
export const refreshLockName = 'ao3mirror-session-refresh';

/** Statuses that never have a body. */
const noBody = new Set([204, 205, 304]);

/** Methods the API's CSRF check lets through without the header. */
const safeMethods = new Set(['GET', 'HEAD']);

/** One decoded response, the shape every generated response type has. */
interface Decoded {
	data: unknown;
	status: number;
	headers: Headers;
}

/** A request ready to send, and to send again after a refresh. */
interface Prepared {
	url: string;
	init: RequestInit;
}

const sessionEvents = new EventTarget();

/**
 * Calls `listener` whenever the session turns out to be gone: a request wasn't signed in and
 * the refresh was refused, so only a new login helps. Returns the function that unsubscribes.
 */
export function onSessionLost(listener: () => void): () => void {
	sessionEvents.addEventListener('lost', listener);
	return () => {
		sessionEvents.removeEventListener('lost', listener);
	};
}

/** One API response as the generated types see it: `T` is a union keyed by status. */
export async function apiFetch<T>(url: string, init: RequestInit): Promise<T> {
	const req = prepare(url, init);
	let res = await send(req);
	if (notSignedIn(res)) {
		res = await navigator.locks.request(refreshLockName, () => refreshAndRetry(req));
	}
	return res as T;
}

/**
 * Runs while holding the refresh lock, after `req` came back 401 `unauthorized`. Returns the
 * response to hand the caller: the retried request's after a refresh, else the 401.
 */
async function refreshAndRetry(req: Prepared): Promise<Decoded> {
	// Another tab may have refreshed while this one waited for the lock, and the cookies are
	// shared: try again before spending the refresh cookie.
	const again = await send(req);
	if (!notSignedIn(again)) {
		return again;
	}
	const refreshed = await send(
		prepare(refreshPath, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ transport: 'cookie' })
		})
	);
	if (refreshed.status === 200) {
		return send(req);
	}
	// 401 means the session is over (expired, revoked, or no refresh cookie). Anything else (a
	// rate limit, a server error) says nothing about the session, so it isn't reported as lost.
	if (refreshed.status === 401) {
		sessionEvents.dispatchEvent(new Event('lost'));
	}
	return again;
}

/** Whether the API refused the request for having no valid access token. */
function notSignedIn(res: Decoded): boolean {
	return res.status === 401 && problemSlug(res.data) === 'unauthorized';
}

/**
 * Adds the reveal param (a signed-in user reads their own mirror; docs/part4-ui.md "Fanwork
 * protection in the UI") and, on unsafe methods, the CSRF header. The spec declares no reveal
 * param, so a generated URL never has one already.
 */
function prepare(url: string, init: RequestInit): Prepared {
	const headers = new Headers(init.headers);
	if (!safeMethods.has((init.method ?? 'GET').toUpperCase())) {
		headers.set(csrfHeader, '1');
	}
	const sep = url.includes('?') ? '&' : '?';
	return { url: `${url}${sep}${REVEAL_PARAM}=${REVEAL_VALUE}`, init: { ...init, headers } };
}

/** Sends the request and decodes the response. Generated bodies are strings, so a retry can resend them. */
async function send(req: Prepared): Promise<Decoded> {
	const res = await fetch(req.url, req.init);
	const text = noBody.has(res.status) ? '' : await res.text();
	const data: unknown = text === '' ? undefined : JSON.parse(text);
	return { data, status: res.status, headers: res.headers };
}
