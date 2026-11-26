import { defineConfig } from 'orval';

// The typed API client (docs/part4-ui.md, "API client"), generated from the hand-written spec by
// `make generate`. Each operation is a plain function that calls apiFetch, so every request goes
// through one place, which adds the reveal param and the CSRF header and refreshes the session.
export default defineConfig({
	api: {
		input: { target: '../docs/api/openapi.yaml' },
		output: {
			target: 'src/lib/api/api.gen.ts',
			client: 'fetch',
			mode: 'single',
			override: { mutator: { path: 'src/lib/api/http.ts', name: 'apiFetch' } }
		}
	}
});
