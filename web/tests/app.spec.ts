import { expect, problems, test, uiCSP } from './fixtures';

// The login page makes no API request, so nothing (not even a 401) reaches the console.
test('the built app loads under the UI CSP', async ({ page }) => {
	const seen = problems(page);
	const response = await page.goto('/login');
	expect(response?.status()).toBe(200);
	expect(response?.headers()['content-security-policy']).toBe(uiCSP);
	await expect(page).not.toHaveTitle(/UI not built/);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in');
	expect(seen).toEqual([]);
});

// The server answers any non-API path with the app (SPA fallback), and the app's router decides:
// a path that is no route gets the router's own 404 page, rendered in the browser.
test('a deep link gets the app, whose router handles it', async ({ page }) => {
	const seen = problems(page);
	const response = await page.goto('/no/such/page');
	expect(response?.status()).toBe(200);
	expect(response?.headers()['content-security-policy']).toBe(uiCSP);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('404');
	expect(seen).toEqual([]);
});

test('the browser enforces Trusted Types', async ({ page }) => {
	await page.goto('/');
	const result = await page.evaluate(() => {
		try {
			document.body.innerHTML = '<b>not trusted</b>';
			return 'assigned';
		} catch (err) {
			return err instanceof TypeError ? 'refused' : String(err);
		}
	});
	expect(result).toBe('refused');
});

test('the API answers on the same origin', async ({ request, server }) => {
	const response = await request.get('/api/v1/healthz');
	expect(response.ok()).toBe(true);
	expect(server.works.length).toBeGreaterThan(0);
});
