// How a work's metadata reads on the page (docs/part4-ui.md stage 8): tag groups in AO3's
// order, the chapter count, the byline and the partial-series note. Pure functions, so the
// wording is unit-tested.

import type { TagType, WorkCreator, WorkSeries, WorkStatus, WorkTag } from '#lib/api/api.gen.ts';

/** The tag groups in the order AO3's work page shows them, with its labels. */
const tagGroups: readonly { type: TagType; label: string; plural: string }[] = [
	{ type: 'Rating', label: 'Rating', plural: 'Ratings' },
	{ type: 'ArchiveWarning', label: 'Archive Warning', plural: 'Archive Warnings' },
	{ type: 'Category', label: 'Category', plural: 'Categories' },
	{ type: 'Fandom', label: 'Fandom', plural: 'Fandoms' },
	{ type: 'Relationship', label: 'Relationship', plural: 'Relationships' },
	{ type: 'Character', label: 'Character', plural: 'Characters' },
	{ type: 'Freeform', label: 'Additional Tag', plural: 'Additional Tags' },
	{ type: 'Media', label: 'Media', plural: 'Media' },
	{ type: 'Unknown', label: 'Other Tag', plural: 'Other Tags' }
];

/** One group of a work's tags, ready to show. */
export interface TagGroup {
	type: TagType;
	label: string;
	tags: WorkTag[];
}

/**
 * The work's tags grouped by type, in AO3's order, each group in the work's own order. Empty
 * groups are left out. A type this UI doesn't know yet goes with the other tags.
 */
export function groupTags(tags: readonly WorkTag[]): TagGroup[] {
	const known = new Set(tagGroups.map((g) => g.type));
	const byType = new Map<TagType, WorkTag[]>();
	for (const tag of tags) {
		const type = known.has(tag.type) ? tag.type : 'Unknown';
		byType.set(type, [...(byType.get(type) ?? []), tag]);
	}
	return tagGroups.flatMap((g) => {
		const group = byType.get(g.type);
		if (group === undefined) {
			return [];
		}
		return [{ type: g.type, label: group.length === 1 ? g.label : g.plural, tags: group }];
	});
}

/** The tag types a listing blurb shows after the fandoms, in AO3's order (`blurb_tag_block`). */
const blurbTypes: readonly TagType[] = ['ArchiveWarning', 'Relationship', 'Character', 'Freeform'];

/**
 * The tags a listing blurb shows after its fandoms, as AO3's `blurb_tag_block` does: the
 * warnings (which AO3 shows in bold, including "No Archive Warnings Apply"), relationships,
 * characters, then additional tags, each type in the work's own order.
 */
export function blurbTags(tags: readonly WorkTag[]): WorkTag[] {
	return blurbTypes.flatMap((type) => tags.filter((t) => t.type === type));
}

/** AO3's chapter count: "3/4", or "3/?" when the expected count is unknown. */
export function chapterCount(posted: number, expected: number | undefined): string {
	return `${String(posted)}/${expected === undefined ? '?' : String(expected)}`;
}

/** A creator as AO3's byline shows one: the pseud, with the login after it when they differ. */
export function creatorName(c: WorkCreator): string {
	return c.pseud === c.user ? c.pseud : `${c.pseud} (${c.user})`;
}

/** "2", "2 and 4", "2, 4 and 5". */
export function listNumbers(ns: readonly number[]): string {
	const s = ns.map(String);
	if (s.length <= 1) {
		return s.join('');
	}
	return `${s.slice(0, -1).join(', ')} and ${s.at(-1) ?? ''}`;
}

/**
 * What the partial-series banner says about a series the work is in, or undefined when the
 * whole series is mirrored (or its page was never fetched, so nothing is known to be missing).
 * It names the missing part numbers when it knows them, else counts the works.
 */
export function seriesGap(s: WorkSeries): string | undefined {
	if (s.complete_locally) {
		return undefined;
	}
	const n = s.missing_positions.length;
	if (n > 0) {
		return `${n === 1 ? 'Part' : 'Parts'} ${listNumbers(s.missing_positions)} ${n === 1 ? 'is' : 'are'} not mirrored.`;
	}
	if (s.works_total === null) {
		return undefined;
	}
	return `${String(s.works_mirrored)} of its ${String(s.works_total)} works are mirrored.`;
}

/**
 * The date AO3 shows after the published date: "Completed" for a finished work with more than
 * one chapter, "Updated" for one in progress. Nothing when it is the published date (a
 * one-shot) or unknown.
 */
export function revisedLabel(w: {
	published_on?: string;
	revised_on?: string;
	complete: boolean;
}): string | undefined {
	if (w.revised_on === undefined || w.revised_on === w.published_on) {
		return undefined;
	}
	return w.complete ? 'Completed' : 'Updated';
}

/** What the work page says about a work that isn't simply on AO3 any more, if anything. */
export function statusNote(status: WorkStatus): string | undefined {
	switch (status) {
		case 'ok':
			return undefined;
		case 'deleted':
			return 'This work was deleted from AO3. This is the copy the mirror kept.';
		case 'hidden':
			return 'AO3 has hidden this work. This is the copy the mirror kept.';
		case 'restricted':
			return 'This work is only shown to signed-in AO3 users.';
		case 'unrevealed':
			return 'This work is in an unrevealed AO3 collection.';
		default:
			return undefined;
	}
}

/** An API ID (a positive safe integer, the spec's ID) written in decimal, as a route has it. */
export function parseId(raw: string): number | undefined {
	if (!/^[1-9]\d{0,15}$/.test(raw)) {
		return undefined;
	}
	const n = Number(raw);
	return Number.isSafeInteger(n) ? n : undefined;
}
