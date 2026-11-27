// The route param matchers. `[id=id]` is an API ID, which the page gets as a number; any other
// path segment there is no route (the router's 404).

import { defineParams } from '@sveltejs/kit/params';

import { parseId } from '#lib/work/meta.ts';

export const params = defineParams({
	id: parseId
});
