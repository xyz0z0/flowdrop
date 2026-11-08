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
export const ADMIN_PASSWORD = 'e2e-correct-horse-staple';

/** Sets the admin password for a data dir (non-interactive). */
export function initPassword(dataDir: string, password = ADMIN_PASSWORD): void {
  const res = spawnSync(HUB_BIN, ['admin', 'init', '--password-stdin'], {
    env: { ...process.env, FLOWDROP_DATA_DIR: dataDir },
    input: `${password}\n${password}\n`,
    encoding: 'utf8',
  });
  if (res.status !== 0) {
    throw new Error(`admin init failed (${res.status}): ${res.stderr}`);
  }
}

/** Boots a real Hub binary against a throwaway data dir. */
export async function startHub(port: number, opts: { dataDir?: string } = {}): Promise<Hub> {
  const dataDir = opts.dataDir ?? mkdtempSync(join(tmpdir(), 'flowdrop-e2e-'));
  const ownDir = opts.dataDir === undefined;
  initPassword(dataDir);

  const child: ChildProcess = spawn(HUB_BIN, ['serve'], {
    env: {
      ...process.env,
      FLOWDROP_DATA_DIR: dataDir,
      FLOWDROP_LISTEN: `127.0.0.1:${port}`,
      // http://127.0.0.1 is a browser secure context, so cookies still work.
      FLOWDROP_COOKIE_SECURE: 'false',
      FLOWDROP_MAX_UPLOAD_BYTES: String(2 * 1024 * 1024),
      FLOWDROP_LOGIN_LIMIT_PER_MINUTE: '50',
      FLOWDROP_LOG_LEVEL: 'warn',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  child.stderr?.on('data', (d: Buffer) => {
    if (process.env.FLOWDROP_E2E_VERBOSE) process.stderr.write(d);
  });

  const baseURL = `http://127.0.0.1:${port}`;
  await waitForHealth(baseURL, 20_000);

  return {
    baseURL,
    dataDir,
    stop: async () => {
      if (child.exitCode === null) {
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

async function waitForHealth(base: string, ms: number): Promise<void> {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) {
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
