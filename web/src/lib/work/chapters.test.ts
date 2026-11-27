import { describe, expect, it } from 'vitest';

import { chapterHeading, chapterPlace, isChaptered, maxPosition, parsePosition } from './chapters';

describe('parsePosition', () => {
	it("takes 1 up to the API's bound", () => {
		expect(parsePosition('1')).toBe(1);
		expect(parsePosition(String(maxPosition))).toBe(maxPosition);
		for (const bad of ['', '0', '01', '-1', '1.5', 'abc', String(maxPosition + 1)]) {
			expect(parsePosition(bad), bad).toBeUndefined();
		}
	});
});

describe('chapterHeading', () => {
	it('adds the title when there is one', () => {
		expect(chapterHeading({ position: 2, title: 'Fixture Chapter Two' })).toBe('Chapter 2: Fixture Chapter Two');
		expect(chapterHeading({ position: 3 })).toBe('Chapter 3');
		expect(chapterHeading({ position: 4, title: '  ' })).toBe('Chapter 4');
	});
});

describe('isChaptered', () => {
	it('is a work of several chapters, or one that expects other than one', () => {
		expect(isChaptered(1, 1)).toBe(false);
		expect(isChaptered(2, 2)).toBe(true);
		expect(isChaptered(1, 3)).toBe(true);
		expect(isChaptered(1, undefined)).toBe(true);
	});
});

describe('chapterPlace', () => {
	const chapters = [{ position: 1 }, { position: 2 }, { position: 4 }];

	it('finds the neighbours in the list', () => {
		expect(chapterPlace(chapters, 1)).toEqual({ previous: undefined, next: 2, first: true, last: false });
		expect(chapterPlace(chapters, 2)).toEqual({ previous: 1, next: 4, first: false, last: false });
		// Position 3 isn't stored: the list steps from 2 to 4.
		expect(chapterPlace(chapters, 4)).toEqual({ previous: 2, next: undefined, first: false, last: true });
	});

	it('is undefined for a position the list lacks', () => {
		expect(chapterPlace(chapters, 3)).toBeUndefined();
		expect(chapterPlace([], 1)).toBeUndefined();
	});

	it('is both first and last for a one-shot', () => {
		expect(chapterPlace([{ position: 1 }], 1)).toEqual({
			previous: undefined,
			next: undefined,
			first: true,
			last: true
		});
	});
});
