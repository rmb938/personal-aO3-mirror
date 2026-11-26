import { rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import type { Locator, Page } from '@playwright/test';

import { expect, test } from './fixtures';
import { enterPassword, enterSecondFactor, expectSignedIn } from './signin';
import { totp } from './totp';

// Screenshots of every page and notable state, at desktop and phone width, for pull requests
// that change the UI (CLAUDE.md "Dev workflow"; docs/part4-ui.md "E2E harness"). They run with
// the other end-to-end tests, so `make check` keeps web/screenshots/ current, and CI keeps
// them as an artifact. Everything shown is the seed's synthetic content.
//
// A new page or state gets a shot here in the same change. The files are named
// <page>-<state>-<viewport>.png; the directory is emptied first, so it never holds shots of
// states that are gone.
//
// They use the seeded `screenshots` account, which nothing else touches, and run in order.

const dir = fileURLToPath(new URL('../screenshots/', import.meta.url));

const viewports = {
	desktop: { width: 1280, height: 900 },
	phone: { width: 390, height: 844 }
} as const;

/** Saves `name` at each viewport: the whole page, or just `part` of it. */
async function shoot(page: Page, name: string, part?: Locator): Promise<void> {
	for (const [viewport, size] of Object.entries(viewports)) {
		await page.setViewportSize(size);
		const path = `${dir}${name}-${viewport}.png`;
		await (part === undefined ? page.screenshot({ path, fullPage: true }) : part.screenshot({ path }));
	}
	await page.setViewportSize(viewports.desktop);
}

test.describe.configure({ mode: 'serial' });

test.beforeAll(async () => {
	await rm(dir, { recursive: true, force: true });
});

test('login and account pages', async ({ page, server }) => {
	const account = server.accounts.screenshots;

	// Login: the password step, then both second factors (a recovery code signs in, so the one
	// TOTP code this test types later is the account's first in its 30 s step).
	await page.goto('/login');
	await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
	await shoot(page, 'login-password');
	await enterPassword(page, account);
	await expect(page.getByLabel('Code from your authenticator app')).toBeVisible();
	await shoot(page, 'login-totp');
	await enterSecondFactor(page, account, { recoveryCode: 0 });
	await expectSignedIn(page, account);
	await shoot(page, 'home');

	// Account page.
	await page.getByRole('link', { name: 'Account' }).click();
	await expect(page.getByRole('region', { name: "Where you're signed in" }).getByRole('listitem')).toHaveCount(1);
	await shoot(page, 'account');

	// A refused new password (the policy is checked first, so this costs no attempt).
	const form = page.getByRole('region', { name: 'Change your password' });
	await form.getByLabel('Current password').fill(account.password);
	await form.getByLabel('New password', { exact: true }).fill(account.username);
	await form.getByLabel('New password again').fill(account.username);
	await form.getByLabel('Code from your authenticator app').fill('000000');
	await form.getByRole('button', { name: 'Change password' }).click();
	await expect(form.getByRole('alert')).toBeVisible();
	await shoot(page, 'account-password-refused', form);

	// New recovery codes, shown once.
	const recovery = page.getByRole('region', { name: 'Recovery codes', exact: true });
	await recovery.getByLabel('Code from your authenticator app').fill(totp(account.totp_secret));
	await recovery.getByRole('button', { name: 'Make new recovery codes' }).click();
	await expect(recovery.getByRole('list', { name: 'Recovery codes' }).getByRole('listitem')).toHaveCount(10);
	await shoot(page, 'account-recovery-codes', recovery);

	// Last, since it backs the account off: a wrong password at the login page.
	await page.getByRole('button', { name: 'Log out' }).click();
	await expect(page).toHaveURL('/login');
	await enterPassword(page, account, 'not-the-password');
	await expect(page.getByRole('alert')).toBeVisible();
	await shoot(page, 'login-wrong-password');
});
