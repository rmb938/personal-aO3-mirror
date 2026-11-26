// RFC 9457 problems as the UI reads them (docs/api/problems.md). Each problem's `type` is a URI
// ending in `#<slug>`; the slug is the stable part to branch on, never the `detail` text.

/** The problem type's slug (`unauthorized`, `rate-limited`, ...), or undefined if `data` is no problem. */
export function problemSlug(data: unknown): string | undefined {
	if (typeof data !== 'object' || data === null || !('type' in data) || typeof data.type !== 'string') {
		return undefined;
	}
	const hash = data.type.lastIndexOf('#');
	return hash < 0 ? undefined : data.type.slice(hash + 1);
}
