import js from '@eslint/js';
import { defineConfig, globalIgnores } from 'eslint/config';
import svelte from 'eslint-plugin-svelte';
import globals from 'globals';
import ts from 'typescript-eslint';

// The one component allowed to use {@html} (docs/part4-ui.md, "Security model").
const trustedHtmlComponent = 'src/lib/html/TrustedHtml.svelte';

const rawHtmlSinks = ['innerHTML', 'outerHTML', 'insertAdjacentHTML', 'setHTMLUnsafe', 'write', 'writeln'];

export default defineConfig(
	globalIgnores(['build/', '.svelte-kit/', 'node_modules/', 'test-results/', 'playwright-report/', 'src/lib/api/api.gen.ts']),
	js.configs.recommended,
	ts.configs.strictTypeChecked,
	ts.configs.stylisticTypeChecked,
	svelte.configs.recommended,
	{
		languageOptions: {
			parserOptions: {
				projectService: true,
				extraFileExtensions: ['.svelte'],
				tsconfigRootDir: import.meta.dirname
			}
		}
	},
	{
		files: ['**/*.svelte', '**/*.svelte.ts', '**/*.svelte.js'],
		languageOptions: { parserOptions: { parser: ts.parser } }
	},
	{
		files: ['src/**'],
		languageOptions: { globals: globals.browser },
		rules: {
			// HTML reaches the page only through TrustedHtml.svelte (sanitizer output, a
			// TrustedHTML), never by a string sink.
			'svelte/no-at-html-tags': 'error',
			// The CSP (`style-src 'self'`) blocks style attributes, and transitions inject
			// <style> elements: style with classes in component CSS instead.
			'svelte/no-inline-styles': ['error', { allowTransitions: false }],
			'no-restricted-properties': [
				'error',
				...rawHtmlSinks.map((property) => ({
					property,
					message: 'Render sanitized HTML with TrustedHtml.svelte instead.'
				}))
			]
		}
	},
	{
		files: [trustedHtmlComponent],
		rules: { 'svelte/no-at-html-tags': 'off' }
	},
	{
		// Playwright tests run in Node (their page.evaluate callbacks get the DOM types from
		// tsconfig).
		files: ['scripts/**', 'tests/**', '*.js', '*.ts'],
		languageOptions: { globals: globals.node }
	}
);
