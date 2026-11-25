// `svelte-check --tsgo` type-checks Svelte files transpiled into .svelte-kit/.svelte-check, but
// never removes the copy of a deleted or renamed component, which then keeps reporting errors.
// Clearing it first makes every check start clean (it only costs re-transpiling).
import { rmSync } from 'node:fs';

rmSync('.svelte-kit/.svelte-check', { recursive: true, force: true });
