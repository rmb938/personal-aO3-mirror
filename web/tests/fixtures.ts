import { test as base, type Page } from '@playwright/test';

// problems collects everything the page logs or throws from now on. The UI is meant to run
// with none: a CSP or Trusted Types violation shows up here as a console error (and so does a
// failed request, such as the 401 before signing in).
export function problems(page: Page): string[] {
	const seen: string[] = [];
	page.on('console', (msg) => seen.push(`console ${msg.type()}: ${msg.text()}`));
	page.on('pageerror', (err) => seen.push(`page error: ${err.message}`));
	return seen;
}

// The environment variable global-setup.ts passes the server's start-up line in.
export const serverEnv = 'AO3MIRROR_E2E_SERVER';

// A seeded account's credentials.
export interface Account {
	username: string;
	password: string;
	totp_secret: string;
	recovery_codes: string[];
}

// The seeded accounts (internal/e2eseed.Accounts), one per group of tests, so a test that
// changes an account (password, recovery codes, sessions, backoff, last TOTP step) never
// disturbs another group.
export type AccountKey = 'reader' | 'sessions' | 'password' | 'recovery';

// What `ao3mirror dev ui-server` prints once listening (cmd/ao3mirror/uiserver.go). All of it
// is synthetic: the accounts exist only in that run's temporary data dir.
export interface Server {
	url: string;
	accounts: Record<AccountKey, Account>;
	works: number[];
	series: number[];
}

function server(): Server {
	const line = process.env[serverEnv];
	if (!line) {
		throw new Error(`${serverEnv} is not set: global-setup.ts did not run`);
	}
	return JSON.parse(line) as Server;
}

// The UI's Content-Security-Policy (docs/part4-ui.md "Security model", internal/web/ui.CSP).
export const uiCSP =
	"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; " +
	"object-src 'none'; base-uri 'none'; frame-src 'none'; frame-ancestors 'none'; form-action 'self'; " +
	"require-trusted-types-for 'script'; trusted-types ao3mirror-html svelte-trusted-html";

// test is Playwright's, with baseURL set to the server and `server` giving its details.
// Playwright requires a fixture's first parameter to be an object pattern, even when empty.
/* eslint-disable no-empty-pattern */
export const test = base.extend<{ server: Server }>({
	server: async ({}, use) => {
		await use(server());
	},
	baseURL: async ({}, use) => {
		await use(server().url);
	}
});
/* eslint-enable no-empty-pattern */

export { expect } from '@playwright/test';
