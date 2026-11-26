import type { Page } from '@playwright/test';

import { expect } from './fixtures';
import { totp } from './totp';

// Steps the signup and reset tests share: the TOTP setup a new secret shows
// (src/lib/auth/TotpSetup.svelte).

/** The new secret the setup step shows, as an authenticator app would take it. */
export async function shownSecret(page: Page): Promise<string> {
	const key = page.getByLabel('Authenticator key', { exact: true });
	await expect(key).toHaveText(/^[A-Z2-7]{4}( [A-Z2-7]{4}){7}$/);
	return (await key.innerText()).replaceAll(' ', '');
}

/** Checks the setup step: the QR code, the otpauth link for a phone, the key as text. */
export async function expectTotpSetup(page: Page, username: string): Promise<string> {
	await expect(page.getByRole('heading', { name: 'Add this account to your authenticator app' })).toBeVisible();
	await expect(page.getByRole('img', { name: 'QR code of the new authenticator key' })).toBeVisible();
	const secret = await shownSecret(page);
	const link = page.getByRole('link', { name: 'Open it in your authenticator app' });
	await expect(link).toHaveAttribute(
		'href',
		new RegExp(`^otpauth://totp/[^:]+:${username}\\?secret=${secret}&issuer=`)
	);
	return secret;
}

/** A 6-digit code that `secret` doesn't give now (nor in the neighboring steps). */
export function wrongCode(secret: string): string {
	const now = Date.now();
	const valid = new Set([-30_000, 0, 30_000].map((d) => totp(secret, now + d)));
	for (let n = 0; ; n++) {
		const code = String(n).padStart(6, '0');
		if (!valid.has(code)) {
			return code;
		}
	}
}

/** The recovery codes the page shows once. */
export async function shownRecoveryCodes(page: Page): Promise<string[]> {
	const items = page.getByRole('list', { name: 'Recovery codes' }).getByRole('listitem');
	await expect(items).toHaveCount(10);
	return (await items.allInnerTexts()).map((s) => s.trim());
}
