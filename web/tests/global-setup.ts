import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';

import { serverEnv } from './fixtures';

// How long the server may take to seed and start listening.
const startTimeoutMs = 60_000;
// How long it may take to shut down once its stdin closes.
const stopTimeoutMs = 15_000;

// Starts `ao3mirror dev ui-server` for the whole run and returns the teardown that stops it.
// The server's one stdout line (URL, account, seeded IDs) reaches the tests through an
// environment variable, which Playwright's workers inherit. The server stops when its stdin
// closes, so it also goes away if this process dies.
export default async function globalSetup(): Promise<() => Promise<void>> {
	const bin = process.env['AO3MIRROR_E2E_BIN'];
	if (!bin) {
		throw new Error('AO3MIRROR_E2E_BIN is not set: run the end-to-end tests with `make ui-e2e`');
	}
	const server = spawn(bin, ['dev', 'ui-server'], { stdio: ['pipe', 'pipe', 'inherit'] });
	const exited = new Promise<number | null>((resolve) => server.once('exit', resolve));

	const firstLine = new Promise<string>((resolve, reject) => {
		const lines = createInterface({ input: server.stdout });
		lines.once('line', (line) => {
			lines.close();
			server.stdout.resume(); // nothing more is expected; never let the pipe fill
			resolve(line);
		});
		server.once('error', reject);
		void exited.then((code) => {
			reject(new Error(`ui-server exited (code ${String(code)}) before it was ready`));
		});
		setTimeout(() => {
			reject(new Error(`ui-server not ready after ${String(startTimeoutMs)}ms`));
		}, startTimeoutMs).unref();
	});

	const stop = async (): Promise<void> => {
		if (server.exitCode !== null) {
			return;
		}
		server.stdin.end();
		const timer = setTimeout(() => server.kill('SIGKILL'), stopTimeoutMs);
		const code = await exited;
		clearTimeout(timer);
		if (code !== 0) {
			throw new Error(`ui-server exited with code ${String(code)}`);
		}
	};

	try {
		process.env[serverEnv] = await firstLine;
	} catch (err) {
		server.kill('SIGKILL');
		throw err;
	}
	return stop;
}
