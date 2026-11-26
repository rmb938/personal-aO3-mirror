import type { BrowserContext, Page } from '@playwright/test';

import { expect, type Server, test } from './fixtures';
import { totp } from './totp';

// Login, logout and session refresh in the browser (docs/part4-ui.md stage 5b). The tests in
// this file run in order in one worker: a wrong password backs the account off for every
// login until the next full login, which the same test does before it ends.
//
// The server accepts each TOTP code once per 30 s step, so only one test signs in with TOTP;
// the others each use their own recovery code (each works once), by index.

const accessCookie = '__Host-ao3mirror-access';

type Factor = { totp: true } | { recoveryCode: number };

async function enterPassword(page: Page, server: Server, password = server.account.password): Promise<void> {
	await page.getByLabel('Username').fill(server.account.username);
	await page.getByLabel('Password').fill(password);
	await page.getByRole('button', { name: 'Continue' }).click();
}

async function enterSecondFactor(page: Page, server: Server, factor: Factor): Promise<void> {
	if ('totp' in factor) {
		await page.getByLabel('Code from your authenticator app').fill(totp(server.account.totp_secret));
	} else {
		await page.getByRole('button', { name: 'Use a recovery code instead' }).click();
		const code = server.account.recovery_codes[factor.recoveryCode];
		if (code === undefined) {
			throw new Error(`the seed has no recovery code ${String(factor.recoveryCode)}`);
		}
		await page.getByLabel('Recovery code').fill(code);
	}
	await page.getByRole('button', { name: 'Sign in' }).click();
}

async function expectSignedIn(page: Page, server: Server): Promise<void> {
	await expect(page.getByText(`Signed in as ${server.account.username}`)).toBeVisible();
}

/** Signs in from the home page, which sends the browser to the login page first. */
async function signIn(page: Page, server: Server, factor: Factor): Promise<void> {
	await page.goto('/');
	await expect(page).toHaveURL('/login');
	await enterPassword(page, server);
	await enterSecondFactor(page, server, factor);
	await expect(page).toHaveURL('/');
	await expectSignedIn(page, server);
}

async function dropAccessCookie(context: BrowserContext): Promise<void> {
	await context.clearCookies({ name: accessCookie });
	expect((await context.cookies()).map((c) => c.name)).not.toContain(accessCookie);
}

test('signs in with a TOTP code and returns to the page it came from', async ({ page, server }) => {
	await page.goto('/?from=test#top');
	await expect(page).toHaveURL(`/login?next=${encodeURIComponent('/?from=test#top')}`);
	await enterPassword(page, server);
	await enterSecondFactor(page, server, { totp: true });
	await expect(page).toHaveURL('/?from=test#top');
	await expectSignedIn(page, server);

	// The tokens are HttpOnly cookies: the page's script can't see them.
	expect(await page.evaluate(() => document.cookie)).toBe('');
	const cookies = await page.context().cookies();
	expect(cookies.map((c) => c.name).sort()).toEqual([accessCookie, '__Host-ao3mirror-refresh']);
	for (const c of cookies) {
		expect(c).toMatchObject({ httpOnly: true, secure: true, sameSite: 'Strict', path: '/' });
	}
});

test('signs in with a recovery code', async ({ page, server }) => {
	await signIn(page, server, { recoveryCode: 0 });
});

test('a wrong password is refused, and the backoff shows until it passes', async ({ page, server }) => {
	await page.goto('/login');
	await enterPassword(page, server, 'not-the-password');
	await expect(page.getByRole('alert')).toHaveText("That username and password don't match.");

	// The username is backed off for a second: a try now is refused before the password is checked.
	await page.getByRole('button', { name: 'Continue' }).click();
	// (The button counts down meanwhile, but for under a second: too short to check reliably.)
	await expect(page.getByRole('alert')).toHaveText(/^Too many tries\. Try again in \d+ seconds?\.$/);

	// Once it passes, the right password works (and the full login ends the backoff).
	await expect(page.getByRole('button', { name: 'Continue' })).toBeEnabled({ timeout: 10_000 });
	await enterPassword(page, server);
	await enterSecondFactor(page, server, { recoveryCode: 1 });
	await expect(page).toHaveURL('/');
	await expectSignedIn(page, server);
});

test('logging out ends the session', async ({ page, server }) => {
	await signIn(page, server, { recoveryCode: 2 });
	await page.getByRole('button', { name: 'Log out' }).click();
	await expect(page).toHaveURL('/login');
	expect(await page.context().cookies()).toEqual([]);
	await page.goto('/');
	await expect(page).toHaveURL('/login');
});

test('a missing access cookie is refreshed without signing in again', async ({ page, server }) => {
	await signIn(page, server, { recoveryCode: 3 });
	await dropAccessCookie(page.context());
	await page.reload();
	await expect(page).toHaveURL('/');
	await expectSignedIn(page, server);
});

// The API ends a session whose refresh cookie is traded twice, so two tabs that both find the
// access cookie gone must refresh once between them (apiFetch's Web Lock). Each refresh is held
// back for a moment, so the second tab is refused while the first tab's refresh is still in
// flight: without the lock it would send its own.
test('two tabs refreshing together keep the session', async ({ page, server }) => {
	await signIn(page, server, { recoveryCode: 4 });
	const context = page.context();
	const other = await context.newPage();
	await other.goto('/');
	await expectSignedIn(other, server);

	let refreshes = 0;
	await context.route('**/api/v1/auth/refresh?*', async (route) => {
		refreshes++;
		await new Promise((resolve) => setTimeout(resolve, 500));
		await route.continue();
	});
	await dropAccessCookie(context);
	await Promise.all([page.reload(), other.reload()]);
	await expectSignedIn(page, server);
	await expectSignedIn(other, server);
	expect(refreshes).toBe(1);
	await context.unrouteAll();

	// Still the same live session: another refresh works.
	await dropAccessCookie(page.context());
	await page.reload();
	await expect(page).toHaveURL('/');
	await expectSignedIn(page, server);
});
