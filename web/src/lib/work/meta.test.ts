import { describe, expect, it } from 'vitest';

import type { TagType, WorkSeries, WorkTag } from '#lib/api/api.gen.ts';

import {
	blurbTags,
	chapterCount,
	creatorName,
	groupTags,
	listNumbers,
	parseId,
	revisedLabel,
	seriesGap,
	statusNote
} from './meta';

function tag(id: number, type: TagType): WorkTag {
	return { id, type, name: `Fixture Tag ${String(id)}` };
}

describe('groupTags', () => {
	it("groups in AO3's order, keeping each group's order", () => {
		const groups = groupTags([
			tag(1, 'Freeform'),
			tag(2, 'Fandom'),
			tag(3, 'Rating'),
			tag(4, 'Freeform'),
			tag(5, 'ArchiveWarning')
		]);
		expect(groups.map((g) => [g.label, g.tags.map((t) => t.id)])).toEqual([
			['Rating', [3]],
			['Archive Warning', [5]],
			['Fandom', [2]],
			['Additional Tags', [1, 4]]
		]);
	});

	it('puts a type it does not know with the other tags', () => {
		const groups = groupTags([tag(1, 'Unknown'), tag(2, 'Brand New' as TagType)]);
		expect(groups.map((g) => [g.type, g.label, g.tags.map((t) => t.id)])).toEqual([
			['Unknown', 'Other Tags', [1, 2]]
		]);
	});

	it('has no groups for no tags', () => {
		expect(groupTags([])).toEqual([]);
	});
});

describe('blurbTags', () => {
	it("keeps warnings, relationships, characters and additional tags, in AO3's blurb order", () => {
		const tags = blurbTags([
			tag(1, 'Freeform'),
			tag(2, 'Character'),
			tag(3, 'Rating'),
			tag(4, 'Relationship'),
			tag(5, 'ArchiveWarning'),
			tag(6, 'Fandom'),
			tag(7, 'Category'),
			tag(8, 'Character'),
			tag(9, 'ArchiveWarning')
		]);
		expect(tags.map((t) => t.id)).toEqual([5, 9, 4, 2, 8, 1]);
	});
});

describe('chapterCount', () => {
	it('shows the expected count, or ? when unknown', () => {
		expect(chapterCount(3, 4)).toBe('3/4');
		expect(chapterCount(1, 1)).toBe('1/1');
		expect(chapterCount(5, undefined)).toBe('5/?');
	});
});

describe('creatorName', () => {
	it('adds the login only when it differs from the pseud', () => {
		expect(creatorName({ id: 1, user: 'fixture_author', pseud: 'fixture_author' })).toBe('fixture_author');
		expect(creatorName({ id: 2, user: 'fixture_coauthor', pseud: 'fixture_pseud' })).toBe(
			'fixture_pseud (fixture_coauthor)'
		);
	});
});

describe('listNumbers', () => {
	it('joins with commas and a final and', () => {
		expect(listNumbers([])).toBe('');
		expect(listNumbers([2])).toBe('2');
		expect(listNumbers([2, 4])).toBe('2 and 4');
		expect(listNumbers([2, 4, 5])).toBe('2, 4 and 5');
	});
});

describe('seriesGap', () => {
	const series: WorkSeries = {
		id: 1,
		title: 'Fixture Series',
		position: 1,
		complete_locally: false,
		works_total: 4,
		works_mirrored: 2,
		missing_positions: [2, 4],
		missing_work_ids: [1501]
	};

	it('names the missing parts', () => {
		expect(seriesGap(series)).toBe('Parts 2 and 4 are not mirrored.');
		expect(seriesGap({ ...series, missing_positions: [3] })).toBe('Part 3 is not mirrored.');
	});

	it('counts the works when no part number is known missing', () => {
		expect(seriesGap({ ...series, missing_positions: [] })).toBe('2 of its 4 works are mirrored.');
	});

	it('says nothing for a whole series, or one whose page was never fetched', () => {
		expect(seriesGap({ ...series, complete_locally: true, missing_positions: [] })).toBeUndefined();
		expect(seriesGap({ ...series, works_total: null, missing_positions: [] })).toBeUndefined();
	});
});

describe('revisedLabel', () => {
	it("follows AO3's Completed and Updated", () => {
		expect(revisedLabel({ published_on: '2025-01-02', revised_on: '2025-06-07', complete: false })).toBe('Updated');
		expect(revisedLabel({ published_on: '2025-01-02', revised_on: '2025-06-07', complete: true })).toBe('Completed');
		expect(revisedLabel({ published_on: '2025-04-05', revised_on: '2025-04-05', complete: true })).toBeUndefined();
		expect(revisedLabel({ published_on: '2025-04-05', complete: true })).toBeUndefined();
	});

	it('shows the date of a work known only from a listing, which has no published date', () => {
		expect(revisedLabel({ revised_on: '2025-06-07', complete: false })).toBe('Updated');
	});
});

describe('statusNote', () => {
	it('says nothing for a work that is fine, and something for every other status', () => {
		expect(statusNote('ok')).toBeUndefined();
		for (const s of ['deleted', 'hidden', 'restricted', 'unrevealed'] as const) {
			expect(statusNote(s), s).toMatch(/\.$/);
		}
	});
});

describe('parseId', () => {
	it('takes a positive integer only', () => {
		expect(parseId('1001')).toBe(1001);
		expect(parseId('9007199254740991')).toBe(9007199254740991);
		for (const bad of ['', '0', '01', '-1', '1.5', '1e3', 'abc', ' 1', '9007199254740992', '12345678901234567']) {
			expect(parseId(bad), bad).toBeUndefined();
		}
	});
});
