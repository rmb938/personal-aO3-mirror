// Chapter navigation for the reading views (docs/part4-ui.md stage 8b), after otwarchive's
// chapters/show and works/_work_header_navigation. Pure functions, so they are unit-tested.

import type { ChapterRef } from '#lib/api/api.gen.ts';

import { parseId } from './meta.ts';

/** The query param (and value) of the entire-work view, AO3's own, so its links read the same. */
export const fullWorkParam = 'view_full_work';

/** What a work page shows: the entire work, or the chapter at a position. */
export type ReadingView = { kind: 'full' } | { kind: 'chapter'; position: number };

/** The highest chapter position the API takes (the getChapter route's bound). */
export const maxPosition = 100_000;

/** A chapter position written in decimal, as a route has it: 1 up to the API's bound. */
export function parsePosition(raw: string): number | undefined {
	const n = parseId(raw);
	return n !== undefined && n <= maxPosition ? n : undefined;
}

/** A chapter's heading, as AO3's: "Chapter 2", with ": Its Title" when it has one. */
export function chapterHeading(c: { position: number; title?: string }): string {
	const title = c.title?.trim() ?? '';
	return title === '' ? `Chapter ${String(c.position)}` : `Chapter ${String(c.position)}: ${title}`;
}

/**
 * Whether a work reads as chaptered, so its chapters get headings, summaries and notes: like
 * AO3, when more than one chapter is posted or it expects other than one (a "1/?" counts).
 */
export function isChaptered(posted: number, expected: number | undefined): boolean {
	return posted > 1 || expected !== 1;
}

/** Where a chapter sits in the work's chapter list. */
export interface ChapterPlace {
	previous?: number;
	next?: number;
	first: boolean;
	last: boolean;
}

/**
 * Where the chapter at `position` sits in `chapters` (the work's list, in order), with the
 * positions of its neighbours; undefined when the list has no such chapter. Neighbours come from
 * the list, not position ± 1, so a gap in the positions is stepped over.
 */
export function chapterPlace(chapters: readonly ChapterRef[], position: number): ChapterPlace | undefined {
	const i = chapters.findIndex((c) => c.position === position);
	if (i < 0) {
		return undefined;
	}
	return {
		previous: chapters[i - 1]?.position,
		next: chapters[i + 1]?.position,
		first: i === 0,
		last: i === chapters.length - 1
	};
}
