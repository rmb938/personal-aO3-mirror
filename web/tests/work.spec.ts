import type { Page } from '@playwright/test';

import { expect, problems, type Server, test } from './fixtures';
import { signIn } from './signin';

// The home page, the work page's header (docs/part4-ui.md stage 8a) and reading (8b), over the
// seeded works (internal/e2eseed). Each test signs the `works` account in with its own recovery
// code.

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

/** A region by its exact name ("Notes" is not "Chapter Notes"). */
function region(page: Page, name: string) {
	return page.getByRole('region', { name, exact: true });
}

/** A chapter page's path. */
function chapterPath(work: number, position: number): string {
	return `/works/${String(work)}/chapters/${String(position)}`;
}

/** A failed request, as the browser logs it. */
function failedLoad(status: number): string {
	return expect.stringMatching(new RegExp(`^console error: Failed to load resource: .* ${String(status)}`)) as unknown as string;
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
	// AO3's blurb order: warnings (in bold), relationships, characters, additional tags.
	const tags = blurb.getByRole('list', { name: 'Tags' }).getByRole('listitem');
	await expect(tags).toHaveText([
		'Fixture Warning',
		'Fixture Character A/Fixture Character B',
		'Fixture Character A',
		'Fixture Character B',
		'Fixture Freeform One',
		'Fixture Freeform Two'
	]);
	await expect(tags.first()).toHaveCSS('font-weight', '700');
	await expect(tags.nth(1)).toHaveCSS('font-weight', '400');
	// Relationships get AO3's grey background (#eee in light mode); other tags none.
	await expect(tags.nth(1).locator('span')).toHaveCSS('background-color', 'rgb(238, 238, 238)');
	await expect(tags.nth(2).locator('span')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
	await expect(blurb.getByText('Chapters: 3/4')).toBeVisible();
	await expect(blurb.getByText('Words: 4,200')).toBeVisible();

	// A chaptered work opens at its first chapter, as on AO3.
	await blurb.getByRole('link', { name: 'Fixture Work Chaptered' }).click();
	await expect(page).toHaveURL(chapterPath(chaptered, 1));
	await expect(page.getByRole('heading', { name: 'Fixture Work Chaptered', level: 1 })).toBeVisible();
	expect(seen).toEqual([]);
});

test("a work's header: metadata, series, summary and notes", async ({ page, server }) => {
	const seen = await signedIn(page, server, 1);
	const { chaptered } = works(server);
	await page.goto(`/works/${String(chaptered)}`);
	await expect(page).toHaveURL(chapterPath(chaptered, 1));
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

	// The first chapter carries the work's summary and notes; its end notes wait for the last.
	await expect(region(page, 'Summary')).toContainText('Fixture summary.');
	const notes = region(page, 'Notes');
	await expect(notes).toContainText('Fixture work notes.');
	await expect(notes.getByRole('link', { name: 'more notes' })).toHaveAttribute(
		'href',
		`${chapterPath(chaptered, 3)}#work_endnotes`
	);
	await expect(region(page, 'End Notes')).toHaveCount(0);
	expect(seen).toEqual([]);
});

test('reading chapter by chapter', async ({ page, server }) => {
	const seen = await signedIn(page, server, 5);
	const { chaptered } = works(server);
	await page.goto(chapterPath(chaptered, 1));
	const nav = page.getByRole('navigation', { name: 'Chapters', exact: true });
	const end = page.getByRole('navigation', { name: 'Chapters, end' });
	// The top navigation's links, apart from its chapter index (the <ol>).
	const actions = nav.locator('ul').getByRole('link');

	// Chapter 1: its heading, summary and notes, the text with its images, and no way back.
	const one = region(page, 'Chapter 1: Fixture Chapter One');
	await expect(one.getByRole('heading', { level: 2 })).toHaveText('Chapter 1: Fixture Chapter One');
	await expect(region(page, 'Chapter Summary')).toContainText('Fixture chapter summary.');
	// It has no end notes, so no jump to them.
	await expect(region(page, 'Chapter Notes')).toContainText('Fixture chapter notes.');
	await expect(region(page, 'Chapter Notes').getByRole('link')).toHaveCount(0);
	await expect(one).toContainText('Fixture emphasis.');
	await expect(actions).toHaveText(['Entire Work', 'Next Chapter →']);
	await expect(end.getByRole('link')).toHaveText(['↑ Top', 'Next Chapter →']);
	// The two downloaded images come from the mirror; the one still queued is a note.
	for (const alt of ['fixture square', 'fixture shape']) {
		const img = one.getByRole('img', { name: alt });
		await expect(img).toHaveAttribute('src', /^\/api\/v1\/images\/[0-9a-f]{64}\?/);
		await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
	}
	await expect(one.locator('.ao3m-missing-image')).toHaveText('[Image not mirrored: fixture queued]');

	// The chapter index lists every chapter and marks this one.
	await nav.getByText('Chapter Index').click();
	const index = nav.locator('ol').getByRole('listitem');
	await expect(index).toHaveText(['Chapter 1: Fixture Chapter One', 'Chapter 2: Fixture Chapter Two', 'Chapter 3']);
	await expect(nav.getByRole('link', { name: 'Chapter 1: Fixture Chapter One' })).toHaveAttribute('aria-current', 'page');

	// Chapter 2: no summary or notes of the work's, and its notes point to its end notes.
	await end.getByRole('link', { name: 'Next Chapter →' }).click();
	await expect(page).toHaveURL(`${chapterPath(chaptered, 2)}#workskin`);
	await expect(region(page, 'Chapter 2: Fixture Chapter Two')).toBeVisible();
	await expect(region(page, 'Summary')).toHaveCount(0);
	await expect(region(page, 'Notes')).toHaveCount(0);
	await expect(actions).toHaveText(['Entire Work', '← Previous Chapter', 'Next Chapter →']);
	await region(page, 'Chapter Notes').getByRole('link', { name: 'notes' }).click();
	await expect(region(page, 'Chapter End Notes')).toBeInViewport();
	await expect(region(page, 'Chapter End Notes')).toContainText('Fixture chapter end notes.');

	// Chapter 3, the last: an untitled heading, then the work's end notes.
	await nav.getByRole('link', { name: 'Next Chapter →' }).click();
	await expect(page).toHaveURL(`${chapterPath(chaptered, 3)}#workskin`);
	await expect(region(page, 'Chapter 3').getByRole('heading', { level: 2 })).toHaveText('Chapter 3');
	await expect(region(page, 'End Notes')).toContainText('Fixture work end notes.');
	await expect(actions).toHaveText(['Entire Work', '← Previous Chapter']);
	await nav.getByRole('link', { name: '← Previous Chapter' }).click();
	await expect(page).toHaveURL(`${chapterPath(chaptered, 2)}#workskin`);

	// The first chapter's jump to the work's end notes lands on them, on the last chapter.
	await page.goto(chapterPath(chaptered, 1));
	await region(page, 'Notes').getByRole('link', { name: 'more notes' }).click();
	await expect(page).toHaveURL(`${chapterPath(chaptered, 3)}#work_endnotes`);
	await expect(region(page, 'End Notes')).toBeInViewport();
	expect(seen).toEqual([]);
});

test('reading the entire work', async ({ page, server }) => {
	const seen = await signedIn(page, server, 6);
	const { chaptered } = works(server);
	await page.goto(chapterPath(chaptered, 2));
	const nav = page.getByRole('navigation', { name: 'Chapters', exact: true });
	await nav.getByRole('link', { name: 'Entire Work' }).click();
	await expect(page).toHaveURL(`/works/${String(chaptered)}?view_full_work=true`);

	// Every chapter, with the work's summary and notes before them and its end notes after.
	await expect(page.locator('#chapters > section')).toHaveCount(3);
	await expect(page.locator('#chapters h2')).toHaveText([
		'Chapter 1: Fixture Chapter One',
		'Chapter 2: Fixture Chapter Two',
		'Chapter 3'
	]);
	await expect(region(page, 'Summary')).toBeVisible();
	await expect(region(page, 'Notes').getByRole('link', { name: 'more notes' })).toHaveAttribute('href', '#work_endnotes');
	await expect(region(page, 'End Notes')).toContainText('Fixture work end notes.');
	await expect(region(page, 'Chapter End Notes')).toContainText('Fixture chapter end notes.');

	// The index jumps within the page; Chapter by Chapter goes back to the first chapter.
	await nav.getByText('Chapter Index').click();
	await nav.getByRole('link', { name: 'Chapter 3' }).click();
	await expect(page).toHaveURL(`/works/${String(chaptered)}?view_full_work=true#chapter-3`);
	await expect(region(page, 'Chapter 3').getByRole('heading')).toBeInViewport();
	await page.getByRole('navigation', { name: 'Chapters, end' }).getByRole('link', { name: '↑ Top' }).click();
	await expect(nav).toBeInViewport();
	// Unlike previous/next, Chapter by Chapter opens the chapter at the top, as on AO3.
	await page.getByRole('heading', { name: 'Fixture Work Chaptered', level: 1 }).scrollIntoViewIfNeeded();
	await nav.getByRole('link', { name: 'Chapter by Chapter' }).click();
	await expect(page).toHaveURL(chapterPath(chaptered, 1));
	await expect(region(page, 'Chapter 1: Fixture Chapter One')).toBeVisible();
	expect(await page.evaluate(() => window.scrollY)).toBe(0);
	expect(seen).toEqual([]);
});

test('a work too large to show at once is read a chapter at a time', async ({ page, server }) => {
	const seen = await signedIn(page, server, 7);
	const { chaptered } = works(server);
	// The size cap is 16 MiB, too much to seed: answer the entire-work request as the API would
	// for such a work (controllers.ErrWorkTooLarge).
	await page.route(
		(url) => url.pathname === `/api/v1/works/${String(chaptered)}/chapters`,
		(route) =>
			route.fulfill({
				status: 422,
				contentType: 'application/problem+json',
				json: {
					type: 'https://github.com/rmb938/personal-aO3-mirror/blob/main/docs/api/problems.md#work-too-large',
					title: 'Work too large',
					status: 422,
					detail: 'Read this work one chapter at a time.'
				}
			})
	);
	await page.goto(`/works/${String(chaptered)}?view_full_work=true`);
	await expect(page).toHaveURL(chapterPath(chaptered, 1));
	await expect(page.getByRole('note').last()).toHaveText(
		'This work is too large to show all at once, so it is shown one chapter at a time.'
	);
	await expect(region(page, 'Chapter 1: Fixture Chapter One')).toContainText('Fixture emphasis.');
	expect(seen).toEqual([failedLoad(422)]);
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
	// A one-shot is read on the work's own page, with no chapter heading or navigation.
	await expect(page).toHaveURL(`/works/${String(works(server).anonymous)}`);
	await expect(region(page, 'Work text')).toContainText('Lorem ipsum');
	await expect(page.locator('#chapters h2')).toHaveCount(0);
	await expect(page.getByRole('navigation', { name: /Chapters/ })).toHaveCount(0);
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

	// Chapter 2's text carries the rest: a script, a handler, a javascript: link, a frame and
	// an inline style.
	await page.getByRole('navigation', { name: 'Chapters', exact: true }).getByRole('link', { name: 'Next Chapter →' }).click();
	const payloads = region(page, 'Chapter 2: Fixture Chapter Payloads');
	await expect(payloads).toContainText('Fixture overlay attempt');
	await expect(payloads.locator('script, iframe, [style], [onerror]')).toHaveCount(0);
	await expect(payloads.getByText('Fixture javascript link')).not.toHaveAttribute('href', /javascript/);

	expect(await page.evaluate(() => (window as { __ao3mirrorXSS?: unknown }).__ao3mirrorXSS)).toBeUndefined();
	expect(seen).toEqual([]);
});

test("a work or chapter that isn't mirrored, and paths that are none", async ({ page, server }) => {
	const seen = await signedIn(page, server, 4);
	await page.goto('/works/999999');
	await expect(page.getByRole('alert')).toHaveText("This work isn't in your mirror.");
	// The 404 is the API's answer, which the browser logs as a failed request; nothing else.
	expect(seen).toEqual([failedLoad(404)]);

	// A position past the work's chapters: the work's header, then the chapter's 404.
	await page.goto(chapterPath(works(server).chaptered, 9));
	await expect(page.getByRole('alert')).toHaveText("This chapter isn't in your mirror.");
	await expect(page.getByRole('heading', { name: 'Fixture Work Chaptered', level: 1 })).toBeVisible();
	expect(seen).toEqual([failedLoad(404), failedLoad(404)]);

	// Not an ID or position: the router has no such route.
	for (const path of ['/works/not-a-number', `/works/${String(works(server).chaptered)}/chapters/0`]) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toHaveText('404');
	}
});
