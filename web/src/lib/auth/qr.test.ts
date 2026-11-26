import { describe, expect, it } from 'vitest';

import { modulesPath, qr } from './qr';

describe('modulesPath', () => {
	it('draws one unit square per dark module', () => {
		expect(
			modulesPath([
				[true, false],
				[false, true]
			])
		).toBe('M0 0h1v1h-1zM1 1h1v1h-1z');
		expect(modulesPath([[false]])).toBe('');
	});
});

describe('qr', () => {
	it('encodes an otpauth URI with a quiet zone', () => {
		const uri =
			'otpauth://totp/ao3mirror:fixture_user?secret=' +
			'A'.repeat(32) +
			'&issuer=ao3mirror&algorithm=SHA1&digits=6&period=30';
		const code = qr(uri);
		// At least version 1 (21 modules), plus 4 quiet modules on each side.
		expect(code.size).toBeGreaterThanOrEqual(21 + 8);
		expect(code.path).toMatch(/^(M\d+ \d+h1v1h-1z)+$/);
		// Nothing is drawn in the quiet zone.
		for (const m of code.path.matchAll(/M(\d+) (\d+)/g)) {
			const [x, y] = [Number(m[1]), Number(m[2])];
			expect(x).toBeGreaterThanOrEqual(4);
			expect(y).toBeGreaterThanOrEqual(4);
			expect(x).toBeLessThan(code.size - 4);
			expect(y).toBeLessThan(code.size - 4);
		}
	});
});
