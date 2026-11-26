import type { Page } from '@playwright/test';

import { expectTotpSetup, shownRecoveryCodes, wrongCode } from './enroll';
import { type Account, expect, test } from './fixtures';
import { enterPassword, enterSecondFactor, expectSignedIn } from './signin';
import { totp } from './totp';

// The reset page (docs/part4-ui.md stage 6b). Each test uses the seeded reset link of one
// scope, which resets the seeded account of the same key and nothing else.

const newPassword = 'fixture-reset-password-dolor';

async function fillNewPassword(page: Page): Promise<void> {
	await page.getByLabel('New password', { exact: true }).fill(newPassword);
	await page.getByLabel('New password again').fill(newPassword);
}

async function confirmCode(page: Page, code: string): Promise<void> {
	await page.getByLabel('Code from your authenticator app').fill(code);
	await page.getByRole('button', { name: 'Reset account' }).click();
}

/** Signs in on the login page the reset ends on, with recovery code `index` of `account`. */
async function signInAfter(page: Page, account: Account, index: number): Promise<void> {
	await expect(page).toHaveURL('/login');
	await enterPassword(page, account);
	await enterSecondFactor(page, account, { recoveryCode: index });
	await expectSignedIn(page, account);
}

test('an incomplete reset link says so', async ({ page, server }) => {
	// The token without the scope, as if the link was cut short.
	await page.goto(server.links.reset_both.replace(/&scope=.*$/, ''));
	await expect(page.getByRole('alert')).toContainText('This reset link is incomplete');
	await expect(page.getByRole('button', { name: 'Continue' })).toHaveCount(0);
});

test('resets the password and the authenticator app', async ({ page, server }) => {
	const account = server.accounts.reset_both;
	await page.goto(server.links.reset_both);
	await expect(page.getByText('This link sets a new password and a new authenticator app')).toBeVisible();
	await page.getByLabel('New password', { exact: true }).fill(newPassword);
	await page.getByLabel('New password again').fill('something-else-entirely');
	await page.getByRole('button', { name: 'Continue' }).click();
	await expect(page.getByRole('alert')).toHaveText("The passwords don't match.");
	await fillNewPassword(page);
	await page.getByRole('button', { name: 'Continue' }).click();

	const secret = await expectTotpSetup(page, account.username);
	await confirmCode(page, totp(secret));
	await expect(page.getByRole('status')).toHaveText('Your account is reset. All your devices were signed out.');
	const codes = await shownRecoveryCodes(page);
	await page.getByRole('button', { name: "I've saved them" }).click();

	await signInAfter(page, { ...account, password: newPassword, totp_secret: secret, recovery_codes: codes }, 0);
});

test('resets only the password, confirmed with the current app', async ({ page, server }) => {
	const account = server.accounts.reset_password;
	await page.goto(server.links.reset_password);
	await expect(page.getByText('You keep your authenticator app.')).toBeVisible();
	await fillNewPassword(page);
	await page.getByRole('button', { name: 'Continue' }).click();

	// No new secret: the code comes from the account's current one.
	await expect(page.getByLabel('Code from your authenticator app')).toBeVisible();
	await expect(page.getByRole('img', { name: 'QR code of the new authenticator key' })).toHaveCount(0);
	await expect(page.getByLabel('Authenticator key', { exact: true })).toHaveCount(0);
	await confirmCode(page, totp(account.totp_secret));
	await expect(page.getByRole('status')).toHaveText('Your account is reset. All your devices were signed out.');
	await expect(page.getByRole('list', { name: 'Recovery codes' })).toHaveCount(0);
	await page.getByRole('link', { name: 'Sign in' }).click();

	// The seeded recovery codes still work.
	await signInAfter(page, { ...account, password: newPassword }, 0);
});

test('resets only the authenticator app', async ({ page, server }) => {
	const account = server.accounts.reset_totp;
	await page.goto(server.links.reset_totp);
	await expect(page.getByText('You keep your password.')).toBeVisible();
	await expect(page.getByLabel('New password', { exact: true })).toHaveCount(0);
	await page.getByRole('button', { name: 'Continue' }).click();

	const secret = await expectTotpSetup(page, account.username);
	await confirmCode(page, wrongCode(secret));
	await expect(page.getByRole('alert')).toContainText("That code didn't work.");
	await confirmCode(page, totp(secret));
	const codes = await shownRecoveryCodes(page);
	await page.getByRole('button', { name: "I've saved them" }).click();

	await signInAfter(page, { ...account, totp_secret: secret, recovery_codes: codes }, 0);

	// The link is used up.
	await page.goto(server.links.reset_totp);
	await page.getByRole('button', { name: 'Continue' }).click();
	await expect(page.getByRole('alert')).toContainText("This reset link can't be used");
});
