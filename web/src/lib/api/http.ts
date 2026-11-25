// The one function every generated API call goes through (orval.config.ts). It returns what
// the generated response types describe: the decoded body, the status and the headers. Stage 5
// adds the session refresh and retry, the CSRF header and the reveal param here.

/** Statuses that never have a body. */
const noBody = new Set([204, 205, 304]);

/** One API response as the generated types see it: `T` is a union keyed by status. */
export async function apiFetch<T>(url: string, init: RequestInit): Promise<T> {
	const res = await fetch(url, init);
	const text = noBody.has(res.status) ? '' : await res.text();
	const data: unknown = text === '' ? undefined : JSON.parse(text);
	return { data, status: res.status, headers: res.headers } as T;
}
