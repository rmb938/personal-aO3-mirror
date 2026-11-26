import { expectTotpSetup, shownRecoveryCodes, wrongCode } from './enroll';
import { expect, problems, test } from './fixtures';
import { enterPassword, enterSecondFactor, expectSignedIn } from './signin';
import { totp } from './totp';

// The signup page (docs/part4-ui.md stage 6b), through the seeded `signup` invite, which only
// this test uses up.

const newUser = { username: 'e2e_signed_up', password: 'fixture-signup-password-ipsum' };

test('an incomplete invite link says so', async ({ page }) => {
	await page.goto('/signup');
	await expect(page.getByRole('alert')).toContainText('This invite link is incomplete');
	await expect(page.getByLabel('Username')).toHaveCount(0);
});

test('signs up from an invite, then signs in', async ({ page, server }) => {
	await page.goto(server.links.signup);
	await expect(page.getByRole('heading', { name: 'Create your account' })).toBeVisible();
	const form = {
		username: page.getByLabel('Username'),
		password: page.getByLabel('Password', { exact: true }),
		confirm: page.getByLabel('Password again'),
		submit: page.getByRole('button', { name: 'Continue' })
	};
	async function fill(username: string, password: string, confirm = password): Promise<void> {
		await form.username.fill(username);
		await form.password.fill(password);
		await form.confirm.fill(confirm);
		await form.submit.click();
	}

	// Refusals, none of which spends the invite.
	await fill(newUser.username, newUser.password, 'something-else-entirely');
	await expect(page.getByRole('alert')).toHaveText("The passwords don't match.");
	await fill(server.accounts.reader.username, newUser.password);
	await expect(page.getByRole('alert')).toHaveText('That username is taken. Choose another one.');
	await fill(newUser.username, newUser.username);
	await expect(page.getByRole('alert')).toContainText('Choose a different password.');

	// The TOTP setup, with no console messages (the QR code is SVG markup under the CSP).
	const seen = problems(page);
	await fill(newUser.username, newUser.password);
	const secret = await expectTotpSetup(page, newUser.username);
	expect(seen).toEqual([]);

	const code = page.getByLabel('Code from your authenticator app');
	await code.fill(wrongCode(secret));
	await page.getByRole('button', { name: 'Create account' }).click();
	await expect(page.getByRole('alert')).toContainText("That code didn't work.");
	await code.fill(totp(secret));
	await page.getByRole('button', { name: 'Create account' }).click();

	await expect(page.getByRole('status')).toHaveText('Your account is ready.');
	const codes = await shownRecoveryCodes(page);
	await page.getByRole('button', { name: "I've saved them" }).click();
	await expect(page).toHaveURL('/login');

	// The new account signs in (with a recovery code: the TOTP code above used this step).
	const account = { ...newUser, totp_secret: secret, recovery_codes: codes };
	await enterPassword(page, account);
	await enterSecondFactor(page, account, { recoveryCode: 0 });
	await expectSignedIn(page, account);

	// The invite is used up.
	await page.goto(server.links.signup);
	await fill('e2e_signed_up_again', newUser.password);
	await expect(page.getByRole('alert')).toContainText("This invite link can't be used");
});
