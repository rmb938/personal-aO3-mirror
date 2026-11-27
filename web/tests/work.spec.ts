import type { Page } from '@playwright/test';

import { expect, problems, type Server, test } from './fixtures';
import { signIn } from './signin';

// The home page and the work page's header (docs/part4-ui.md stage 8a), over the seeded works
// (internal/e2eseed). Each test signs the `works` account in with its own recovery code.

/** The seeded works, by what they are (internal/e2eseed's constants, in Seeded.works order). */
function works(server: Server) {
	const [chaptered, oneShot, anonymous, hostile] = server.works;
	if (chaptered === undefined || oneShot === undefined || anonymous === undefined || hostile === undefined) {
		throw new Error('the seed has fewer works than these tests expect');
	}
	return { chaptered, oneShot, anonymous, hostile };
}

/** Signs in and starts collecting what the page logs: from here on there must be nothing. */
async function signedIn(page: Page, server: Server, code: number): Promise<string[]> {
	await signIn(page, server.accounts.works, { recoveryCode: code });
	return problems(page);
}

/** The metadata box's value for a label ("Rating:"). */
function meta(page: Page, label: string) {
	return page.locator('dl.meta > dt', { hasText: label }).locator('xpath=following-sibling::dd[1]');
}

test('the home page lists the works, newest first, and links to them', async ({ page, server }) => {
	const seen = await signedIn(page, server, 0);
	const { chaptered } = works(server);
	await expect(page.getByRole('heading', { name: 'Works', level: 1 })).toBeVisible();
	await expect(page.getByText('4 works')).toBeVisible();
	await expect(page.getByRole('heading', { level: 2 })).toHaveText([
		'Fixture Work Chaptered',
		'Fixture Work Anonymous',
		'Fixture Work One-Shot',
		'Fixture Work Hostile'
	]);

	const blurb = page.getByRole('article', { name: 'Fixture Work Chaptered' });
	await expect(blurb.getByText('by fixture_author')).toBeVisible();
	await expect(blurb.getByRole('list', { name: 'Fandoms' })).toHaveText('Fixture Fandom');
	await expect(blurb.getByText('Chapters: 3/4')).toBeVisible();
	await expect(blurb.getByText('Words: 4,200')).toBeVisible();

	await blurb.getByRole('link', { name: 'Fixture Work Chaptered' }).click();
	await expect(page).toHaveURL(`/works/${String(chaptered)}`);
	await expect(page.getByRole('heading', { name: 'Fixture Work Chaptered', level: 1 })).toBeVisible();
	expect(seen).toEqual([]);
});

test("a work's header: metadata, series, summary, notes and chapters", async ({ page, server }) => {
	const seen = await signedIn(page, server, 1);
	await page.goto(`/works/${String(works(server).chaptered)}`);
	await expect(page).toHaveTitle('Fixture Work Chaptered · ao3mirror');
	await expect(page.getByRole('heading', { name: 'Fixture Work Chaptered', level: 1 })).toBeVisible();
	await expect(page.getByText('by fixture_author')).toBeVisible();

	// Tag groups in AO3's order, singular or plural.
	await expect(page.locator('dl.meta > dt')).toHaveText([
		'Rating:',
		'Archive Warning:',
		'Category:',
		'Fandom:',
		'Relationship:',
		'Characters:',
		'Additional Tags:',
		'Language:',
		'Series:',
		'Stats:'
	]);
	await expect(page.getByRole('list', { name: 'Characters' }).getByRole('listitem')).toHaveText([
		'Fixture Character A',
		'Fixture Character B'
	]);
	await expect(meta(page, 'Series:')).toHaveText('Part 1 of Fixture Series Partial');
	const stats = meta(page, 'Stats:');
	await expect(stats.getByRole('term')).toHaveText([
		'Published:',
		'Updated:',
		'Words:',
		'Chapters:',
		'Kudos:',
		'Hits:',
		'Comments:',
		'Bookmarks:'
	]);
	await expect(stats.getByRole('definition')).toHaveText([
		'2025-01-02',
		'2025-06-07',
		'4,200',
		'3/4',
		'120',
		'3,400',
		'15',
		'20'
	]);

	// Parts 2 (known, never fetched) and 4 (unknown) of the series are missing.
	await expect(page.getByRole('note')).toHaveText(
		'This work is in Fixture Series Partial, which is only partly mirrored: Parts 2 and 4 are not mirrored.'
	);

	await expect(page.getByRole('region', { name: 'Summary' })).toContainText('Fixture summary.');
	await expect(page.getByRole('region', { name: 'Notes', exact: true })).toContainText('Fixture work notes.');
	await expect(page.getByRole('region', { name: 'End Notes' })).toContainText('Fixture work end notes.');
	await expect(page.getByRole('region', { name: 'Chapters' }).getByRole('listitem')).toHaveText([
		'Chapter 1: Fixture Chapter One',
		'Chapter 2: Fixture Chapter Two',
		'Chapter 3'
	]);
	expect(seen).toEqual([]);
});

test('an anonymous one-chapter work with no notes or series', async ({ page, server }) => {
	const seen = await signedIn(page, server, 2);
	await page.goto(`/works/${String(works(server).anonymous)}`);
	await expect(page.getByRole('heading', { name: 'Fixture Work Anonymous', level: 1 })).toBeVisible();
	await expect(page.getByText('by Anonymous')).toBeVisible();
	// A one-shot has no Updated date, and nothing here is partly mirrored.
	const terms = meta(page, 'Stats:').getByRole('term');
	await expect(terms.first()).toHaveText('Published:');
	await expect(terms.nth(1)).toHaveText('Words:');
	await expect(meta(page, 'Series:')).toHaveCount(0);
	await expect(page.getByRole('note')).toHaveCount(0);
	await expect(page.getByRole('region', { name: /Notes/ })).toHaveCount(0);
	expect(seen).toEqual([]);
});

// The HTML sanitizer's first run under the real CSP: the hostile work's summary and notes carry
// payloads. None may run, and the browser must report no violation (a blocked inline script or
// a Trusted Types refusal would show up in the console).
test("a hostile work's payloads don't run", async ({ page, server }) => {
	const seen = await signedIn(page, server, 3);
	await page.goto(`/works/${String(works(server).hostile)}`);
	await expect(page.getByRole('heading', { name: 'Fixture Work Hostile', level: 1 })).toBeVisible();
	await expect(page.getByText('by fixture_author, fixture_pseud (fixture_coauthor)')).toBeVisible();
	await expect(meta(page, 'Collections:')).toHaveText('Fixture Collection');
	await expect(meta(page, 'Stats:').getByRole('term').nth(1)).toHaveText('Completed:');
	await expect(meta(page, 'Stats:').getByRole('definition').nth(1)).toHaveText('2025-02-10');

	const summary = page.getByRole('region', { name: 'Summary' });
	await expect(summary).toContainText('Fixture summary with a payload.');
	await expect(summary.locator('script')).toHaveCount(0);
	const notes = page.getByRole('region', { name: 'Notes', exact: true });
	await expect(notes).toContainText('Fixture notes.');
	// The image that isn't mirrored became a note, without its handler.
	await expect(notes.locator('img')).toHaveCount(0);
	await expect(notes.locator('.ao3m-missing-image')).toHaveCount(1);

	expect(await page.evaluate(() => (window as { __ao3mirrorXSS?: unknown }).__ao3mirrorXSS)).toBeUndefined();
	expect(seen).toEqual([]);
});

test("a work that isn't mirrored, and a path that is no work", async ({ page, server }) => {
	const seen = await signedIn(page, server, 4);
	await page.goto('/works/999999');
	await expect(page.getByRole('alert')).toHaveText("This work isn't in your mirror.");
	// The 404 is the API's answer, which the browser logs as a failed request; nothing else.
	expect(seen).toEqual([
		expect.stringMatching(/^console error: Failed to load resource: .* 404/) as unknown as string
	]);

	// Not an ID: the router has no such route.
	await page.goto('/works/not-a-number');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('404');
});
