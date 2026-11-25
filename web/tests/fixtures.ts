import { test as base } from '@playwright/test';

// The environment variable global-setup.ts passes the server's start-up line in.
export const serverEnv = 'AO3MIRROR_E2E_SERVER';

// What `ao3mirror dev ui-server` prints once listening (cmd/ao3mirror/uiserver.go). All of it
// is synthetic: the account exists only in that run's temporary data dir.
export interface Server {
	url: string;
	account: {
		username: string;
		password: string;
		totp_secret: string;
		recovery_codes: string[];
	};
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
