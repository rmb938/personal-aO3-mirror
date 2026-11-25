import { defineConfig, devices } from '@playwright/test';

// End-to-end tests against `ao3mirror dev ui-server`: the real binary, with the built UI
// embedded, over a new data dir seeded with synthetic works (docs/part4-ui.md stage 4). Run
// them with `make ui-e2e`, which builds the binary and points AO3MIRROR_E2E_BIN at it.
export default defineConfig({
	testDir: 'tests',
	globalSetup: './tests/global-setup.ts',
	forbidOnly: !!process.env['CI'],
	retries: 0,
	reporter: 'list',
	use: {
		// The base URL comes from the server (tests/fixtures.ts): it picks a free port.
		trace: 'retain-on-failure'
	},
	projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }]
});
