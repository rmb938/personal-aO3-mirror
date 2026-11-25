import { defineConfig } from 'orval';

// The typed API client (docs/part4-ui.md, "API client"), generated from the hand-written spec by
// `make generate`. Each operation is a plain function that calls apiFetch, so every request goes
// through one place (stage 5 adds refresh, the CSRF header and the reveal param there).
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
