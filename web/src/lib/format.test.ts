import { describe, expect, it } from 'vitest';

import { formatTime } from './format';

describe('formatTime', () => {
	it('formats a timestamp in the given locale', () => {
		const ts = '2026-11-26T15:04:05Z';
		expect(formatTime(ts, 'en-US')).toBe(
			new Date(ts).toLocaleString('en-US', { dateStyle: 'medium', timeStyle: 'short' })
		);
		expect(formatTime(ts, 'en-US')).toMatch(/2026/);
	});

	it('shows an unparsable value as it came', () => {
		expect(formatTime('not a time')).toBe('not a time');
	});
});
