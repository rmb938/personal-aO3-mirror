import { createHmac } from 'node:crypto';

// RFC 6238 TOTP as the server checks it (internal/auth/totp.go): HMAC-SHA1, 6 digits, 30 s
// steps. The tests type the code an authenticator app would show for the seeded account.

const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';

/** RFC 4648 base32, with or without padding, in either case. */
function base32Decode(text: string): Buffer {
	let bits = 0;
	let value = 0;
	const out: number[] = [];
	for (const ch of text.replace(/=+$/, '').toUpperCase()) {
		const i = alphabet.indexOf(ch);
		if (i < 0) {
			throw new Error('not base32');
		}
		value = (value << 5) | i;
		bits += 5;
		if (bits >= 8) {
			bits -= 8;
			out.push((value >>> bits) & 0xff);
		}
	}
	return Buffer.from(out);
}

/** The 6-digit code for `secret` (base32) at `now` (ms since the epoch). */
export function totp(secret: string, now = Date.now()): string {
	const counter = Buffer.alloc(8);
	counter.writeBigUInt64BE(BigInt(Math.floor(now / 1000 / 30)));
	const mac = createHmac('sha1', base32Decode(secret)).update(counter).digest();
	const offset = (mac.at(-1) ?? 0) & 0x0f;
	const bin = mac.readUInt32BE(offset) & 0x7fffffff;
	return String(bin % 1_000_000).padStart(6, '0');
}
