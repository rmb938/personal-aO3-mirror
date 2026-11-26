import { readFile } from 'node:fs/promises';

import type { Browser, Page } from '@playwright/test';

import { type Account, expect, problems, type Server, test } from './fixtures';
import { enterPassword, enterSecondFactor, expectSignedIn, recoveryCode, signIn } from './signin';
import { totp } from './totp';

// The account page (docs/part4-ui.md stage 6). Each test has its own seeded account, since each
// changes it (see fixtures.ts), and signs in with recovery codes so that the one TOTP code it
// types into the page is that account's first in its 30 s step.

const recoveryCodePattern = /^[a-z2-7]{4}(?:-[a-z2-7]{4}){5}$/;

/** Signs the account in a second time, in a browser of its own: another device. */
async function signInElsewhere(browser: Browser, server: Server, account: Account, code: number): Promise<Page> {
	const context = await browser.newContext({ baseURL: server.url });
	const page = await context.newPage();
	await signIn(page, account, { recoveryCode: code });
	return page;
}

async function openAccountPage(page: Page): Promise<void> {
	await page.getByRole('link', { name: 'Account' }).click();
	await expect(page).toHaveURL('/account');
	await expect(page.getByRole('heading', { name: 'Account', level: 1 })).toBeVisible();
}

function sessionsList(page: Page) {
	return page.getByRole('region', { name: "Where you're signed in" }).getByRole('listitem');
}

/** The device is signed out: its next page load goes to the login page. */
async function expectSignedOut(page: Page): Promise<void> {
	await page.reload();
	await expect(page).toHaveURL('/login');
}

test('lists the sessions and signs out another device', async ({ browser, page, server }) => {
	const account = server.accounts.sessions;
	await signIn(page, account, { recoveryCode: 0 });
	const other = await signInElsewhere(browser, server, account, 1);
	await openAccountPage(page);
	await expect(page.getByRole('definition').filter({ hasText: account.username })).toHaveText(account.username);

	const sessions = sessionsList(page);
	await expect(sessions).toHaveCount(2);
	// Newest first: the other device signed in last. This device has no sign-out button.
	await expect(sessions.nth(1)).toHaveAccessibleName('This device');
	await expect(sessions.nth(1).getByRole('button')).toHaveCount(0);

	await sessions.nth(0).getByRole('button', { name: 'Sign out' }).click();
	await expect(sessions).toHaveCount(1);
	await expect(sessions.nth(0)).toHaveAccessibleName('This device');
	await expectSignedOut(other);

	// This device is still signed in.
	await page.reload();
	await expect(sessions).toHaveCount(1);
	await other.context().close();
});

test('changes the password and signs out the other devices', async ({ browser, page, server }) => {
	const account = server.accounts.password;
	const newPassword = 'fixture-new-password-ipsum';
	await signIn(page, account, { recoveryCode: 0 });
	const other = await signInElsewhere(browser, server, account, 1);
	await openAccountPage(page);
	const form = page.getByRole('region', { name: 'Change your password' });
	const fill = async (current: string, next: string, again = next): Promise<void> => {
		await form.getByLabel('Current password').fill(current);
		await form.getByLabel('New password', { exact: true }).fill(next);
		await form.getByLabel('New password again').fill(again);
		await form.getByLabel('Code from your authenticator app').fill(totp(account.totp_secret));
	};
	const submit = form.getByRole('button', { name: 'Change password' });
	const alert = form.getByRole('alert');

	// Checked in the page: the two new passwords differ.
	await fill(account.password, newPassword, `${newPassword}-typo`);
	await submit.click();
	await expect(alert).toHaveText("The new passwords don't match.");

	// The policy is checked first, so this costs no attempt.
	await fill(account.password, account.username);
	await submit.click();
	await expect(alert).toHaveText(/^Choose a different password\. Use 8 to 128 characters\./);

	// A wrong current password is refused and backs the account off for a second.
	await fill('not-the-password', newPassword);
	await submit.click();
	await expect(alert).toHaveText('The current password or the code is wrong.');
	await fill(account.password, newPassword);
	await submit.click();
	await expect(alert).toHaveText(/^Too many tries\. Try again in \d+ seconds?\.$/);

	// Once it passes, the change works.
	await expect(submit).toBeEnabled({ timeout: 10_000 });
	await fill(account.password, newPassword);
	await submit.click();
	await expect(form.getByRole('status')).toHaveText('Your password is changed. Your other devices were signed out.');
	await expect(alert).toHaveCount(0);
	await expect(sessionsList(page)).toHaveCount(1);
	await expectSignedOut(other);
	await other.context().close();

	// The new password signs in; this session stayed signed in until now.
	await page.getByRole('button', { name: 'Log out' }).click();
	await expect(page).toHaveURL('/login');
	await enterPassword(page, account, newPassword);
	await enterSecondFactor(page, account, { recoveryCode: 2 });
	await expect(page).toHaveURL('/');
	await expectSignedIn(page, account);
});

test('makes new recovery codes, shown once, and the old ones stop working', async ({ page, server }) => {
	const account = server.accounts.recovery;
	await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
	await signIn(page, account, { recoveryCode: 0 });
	const seen = problems(page);
	await openAccountPage(page);
	const section = page.getByRole('region', { name: 'Recovery codes' });
	await expect(section).toContainText('You have 9 unused codes left.');

	await section.getByLabel('Code from your authenticator app').fill(totp(account.totp_secret));
	await section.getByRole('button', { name: 'Make new recovery codes' }).click();
	const list = section.getByRole('list', { name: 'Recovery codes' }).getByRole('listitem');
	await expect(list).toHaveCount(10);
	const codes = (await list.allTextContents()).map((c) => c.trim());
	for (const c of codes) {
		expect(c).toMatch(recoveryCodePattern);
	}
	expect(codes).not.toContain(recoveryCode(account, 1));

	await section.getByRole('button', { name: 'Copy' }).click();
	await expect(section.getByText('Copied.')).toBeVisible();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(codes.join('\n'));

	const [download] = await Promise.all([
		page.waitForEvent('download'),
		section.getByRole('link', { name: 'Download' }).click()
	]);
	expect(download.suggestedFilename()).toBe(`ao3mirror-${account.username}-recovery-codes.txt`);
	const file = await readFile(await download.path(), 'utf8');
	expect(file).toContain(account.username);
	for (const c of codes) {
		expect(file).toContain(c);
	}

	// Once saved, they are gone from the page for good.
	await section.getByRole('button', { name: "I've saved them" }).click();
	await expect(list).toHaveCount(0);
	await expect(section).toContainText('You have 10 unused codes left.');
	// The page, the copy and the blob: download ran under the UI CSP without a complaint.
	expect(seen).toEqual([]);

	// An old code no longer signs in; a new one does.
	await page.getByRole('button', { name: 'Log out' }).click();
	await expect(page).toHaveURL('/login');
	await enterPassword(page, account);
	await enterSecondFactor(page, account, { recoveryCode: 1 });
	await expect(page.getByRole('alert')).toHaveText("That recovery code didn't work.");
	// The failure backs the account off for a second: a try now is refused before the code is checked.
	await page.getByLabel('Recovery code').fill(codes[0] ?? '');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page.getByRole('alert')).toHaveText(/^Too many tries\. Try again in \d+ seconds?\.$/);
	await expect(page.getByRole('button', { name: 'Sign in' })).toBeEnabled({ timeout: 10_000 });
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page).toHaveURL('/');
	await expectSignedIn(page, account);
});
