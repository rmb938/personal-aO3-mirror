import type { Page } from '@playwright/test';

import { type Account, expect } from './fixtures';
import { totp } from './totp';

// Signing in through the login page, shared by the test files.
//
// The server accepts each TOTP code once per 30 s step per account, so a test signs in with
// TOTP only when nothing else uses that account's code in the same step; otherwise it uses
// one of the account's recovery codes (each works once), by index.

export type Factor = { totp: true } | { recoveryCode: number };

export async function enterPassword(page: Page, account: Account, password = account.password): Promise<void> {
	await page.getByLabel('Username').fill(account.username);
	await page.getByLabel('Password').fill(password);
	await page.getByRole('button', { name: 'Continue' }).click();
}

/** The account's recovery code number `index`, as seeded. */
export function recoveryCode(account: Account, index: number): string {
	const code = account.recovery_codes[index];
	if (code === undefined) {
		throw new Error(`the seed has no recovery code ${String(index)}`);
	}
	return code;
}

export async function enterSecondFactor(page: Page, account: Account, factor: Factor | { code: string }): Promise<void> {
	if ('totp' in factor) {
		await page.getByLabel('Code from your authenticator app').fill(totp(account.totp_secret));
	} else {
		await page.getByRole('button', { name: 'Use a recovery code instead' }).click();
		const code = 'code' in factor ? factor.code : recoveryCode(account, factor.recoveryCode);
		await page.getByLabel('Recovery code').fill(code);
	}
	await page.getByRole('button', { name: 'Sign in' }).click();
}

export async function expectSignedIn(page: Page, account: Account): Promise<void> {
	await expect(page.getByText(`Signed in as ${account.username}`)).toBeVisible();
}

/** Signs in from the home page, which sends the browser to the login page first. */
export async function signIn(page: Page, account: Account, factor: Factor): Promise<void> {
	await page.goto('/');
	await expect(page).toHaveURL('/login');
	await enterPassword(page, account);
	await enterSecondFactor(page, account, factor);
	await expect(page).toHaveURL('/');
	await expectSignedIn(page, account);
}
