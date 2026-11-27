// The route param matchers. `[id=id]` is an API ID and `[position=position]` a chapter position,
// which the page gets as numbers; any other path segment there is no route (the router's 404).

import { defineParams } from '@sveltejs/kit/params';

import { parsePosition } from '#lib/work/chapters.ts';
import { parseId } from '#lib/work/meta.ts';

export const params = defineParams({
	id: parseId,
	position: parsePosition
});
