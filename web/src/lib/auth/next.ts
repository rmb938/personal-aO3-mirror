// Where to go after signing in. The login page takes it from its `next` query parameter, which
// anyone can put in a link, so only a path on this origin outside the API is followed; anything
// else goes to the home page.

/** The login page's query parameter holding the page to return to. */
export const nextParam = 'next';

/** The login page's URL, remembering `here` (path, query and fragment) unless it is the home page. */
export function loginHref(here: Pick<URL, 'pathname' | 'search' | 'hash'>): string {
	const back = here.pathname + here.search + here.hash;
	return back === '/' ? '/login' : `/login?${new URLSearchParams({ [nextParam]: back }).toString()}`;
}

/** The page to go to after signing in: `raw` if it is a same-origin app path, else "/". */
export function safeNext(raw: string | null, origin: string): string {
	// "//host" and "/\host" are other hosts to a browser; only a single leading slash is a path.
	if (raw === null || !raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\')) {
		return '/';
	}
	let url: URL;
	try {
		url = new URL(raw, origin);
	} catch {
		return '/';
	}
	if (url.origin !== origin || url.pathname === '/login' || url.pathname === '/api' || url.pathname.startsWith('/api/')) {
		return '/';
	}
	return url.pathname + url.search + url.hash;
}
