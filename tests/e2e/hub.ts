import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

export interface Hub {
  baseURL: string;
  dataDir: string;
  stop: () => Promise<void>;
}

export const HUB_BIN =
  process.env.FLOWDROP_BIN ?? join(process.cwd(), '..', 'dist', 'flowdrop');

/**
 * Builds the environment for our own hub/CLI child processes.
 *
 * Every inherited `FLOWDROP_*` variable is dropped first: a developer shell
 * that exports `FLOWDROP_DATA_DIR` or `FLOWDROP_LISTEN` would otherwise point
 * the test hub at a real data dir or a listener owned by the user's instance.
 * Only the variables listed in `extra` reach the child.
 */
export function hubEnv(dataDir: string, extra: Record<string, string> = {}): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (!key.startsWith('FLOWDROP_')) env[key] = value;
  }
  return { ...env, FLOWDROP_DATA_DIR: dataDir, ...extra };
}

/**
 * Creates a fresh four-digit pairing code for an existing data dir by calling
 * the local CLI, exactly like the desktop "添加设备" button. The code is a
 * one-time credential: it is returned to the caller only, never logged, and
 * the CLI's stdout is parsed instead of being echoed.
 */
export function createPairingCode(dataDir: string): string {
  const res = spawnSync(HUB_BIN, ['pair'], {
    env: hubEnv(dataDir),
    encoding: 'utf8',
    timeout: 10_000,
  });
  if (res.error) {
    throw new Error(`pairing code command failed: ${res.error.message}`);
  }
  if (res.status !== 0) {
    // Only stderr is reported; stdout may hold the code and stays unread.
    throw new Error(`pairing code command failed (${res.status}): ${res.stderr?.trim() ?? ''}`);
  }
  const match = /^Pairing code: ([0-9]{4})$/m.exec(res.stdout ?? '');
  if (!match) throw new Error('pairing code command did not print a four-digit code');
  return match[1]!;
}

/** Boots a real Hub binary against a throwaway data dir. */
export async function startHub(port: number, opts: { dataDir?: string } = {}): Promise<Hub> {
  const dataDir = opts.dataDir ?? mkdtempSync(join(tmpdir(), 'flowdrop-e2e-'));
  const ownDir = opts.dataDir === undefined;

  const child: ChildProcess = spawn(HUB_BIN, ['serve'], {
    env: hubEnv(dataDir, {
      FLOWDROP_LISTEN: `127.0.0.1:${port}`,
      // http://127.0.0.1 is a browser secure context, so cookies still work.
      FLOWDROP_COOKIE_SECURE: 'false',
      FLOWDROP_MAX_UPLOAD_BYTES: String(2 * 1024 * 1024),
      FLOWDROP_LOG_LEVEL: 'warn',
    }),
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  child.stderr?.on('data', (d: Buffer) => {
    if (process.env.FLOWDROP_E2E_VERBOSE) process.stderr.write(d);
  });

  let exited = false;
  child.once('exit', () => {
    exited = true;
  });

  const baseURL = `http://127.0.0.1:${port}`;
  try {
    await waitForHealth(child, baseURL, 20_000);
  } catch (err) {
    if (!exited) child.kill('SIGKILL');
    if (ownDir) rmSync(dataDir, { recursive: true, force: true });
    throw err;
  }

  return {
    baseURL,
    dataDir,
    stop: async () => {
      if (!exited && child.exitCode === null && child.signalCode === null) {
        child.kill('SIGTERM');
        await new Promise<void>((resolve) => {
          const timer = setTimeout(() => {
            child.kill('SIGKILL');
            resolve();
          }, 5000);
          child.once('exit', () => {
            clearTimeout(timer);
            resolve();
          });
        });
      }
      if (ownDir) rmSync(dataDir, { recursive: true, force: true });
    },
  };
}

/**
 * Polls /api/v1/health until the hub answers, or fails fast once the child has
 * exited (no point waiting out the deadline for a process that is gone).
 */
async function waitForHealth(child: ChildProcess, base: string, ms: number): Promise<void> {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) {
    if (child.exitCode !== null || child.signalCode !== null) {
      throw new Error(`hub exited before becoming healthy at ${base}`);
    }
    try {
      const res = await fetch(`${base}/api/v1/health`);
      if (res.ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`hub did not become healthy at ${base}`);
}
