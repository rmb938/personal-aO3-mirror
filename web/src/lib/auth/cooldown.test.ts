import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { Cooldown } from './cooldown.svelte.ts';

describe('Cooldown', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	it('counts down whole seconds and stops at 0', () => {
		const c = new Cooldown();
		expect(c.left).toBe(0);
		c.start(2);
		expect(c.left).toBe(2);
		vi.advanceTimersByTime(1100);
		expect(c.left).toBe(1);
		vi.advanceTimersByTime(1000);
		expect(c.left).toBe(0);
		expect(vi.getTimerCount()).toBe(0);
	});

	it('restarts, and stops early', () => {
		const c = new Cooldown();
		c.start(1);
		c.start(5);
		expect(vi.getTimerCount()).toBe(1);
		expect(c.left).toBe(5);
		c.stop();
		expect(c.left).toBe(0);
		expect(vi.getTimerCount()).toBe(0);
	});
});
