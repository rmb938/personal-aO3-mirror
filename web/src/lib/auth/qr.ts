// A QR code as one SVG path, for the TOTP setup step (docs/part4-ui.md "TOTP setup"). uqr
// encodes; the markup is ours, so nothing reaches the page as an HTML string.

import { encode } from 'uqr';

/** A QR code ready to draw: `size` modules square (quiet zone included), dark modules in `path`. */
export interface Qr {
	size: number;
	path: string;
}

/**
 * The path of the dark modules of `matrix` (rows of `true` for dark), one unit square each, in
 * a `0 0 size size` viewBox.
 */
export function modulesPath(matrix: readonly (readonly boolean[])[]): string {
	const parts: string[] = [];
	matrix.forEach((row, y) => {
		row.forEach((dark, x) => {
			if (dark) {
				parts.push(`M${String(x)} ${String(y)}h1v1h-1z`);
			}
		});
	});
	return parts.join('');
}

/**
 * Encodes `text` with medium error correction and the standard four-module quiet zone, which
 * scanners need around the code.
 */
export function qr(text: string): Qr {
	const { size, data } = encode(text, { ecc: 'M', border: 4 });
	return { size, path: modulesPath(data) };
}
