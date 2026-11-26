// Display formatting shared by the pages.

/** An API timestamp (RFC 3339) as a date and time in the browser's locale and time zone. */
export function formatTime(ts: string, locale?: string): string {
	const d = new Date(ts);
	if (Number.isNaN(d.getTime())) {
		return ts;
	}
	return d.toLocaleString(locale, { dateStyle: 'medium', timeStyle: 'short' });
}
