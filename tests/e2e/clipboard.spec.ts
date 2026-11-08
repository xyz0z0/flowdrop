import { test, expect, type Page } from '@playwright/test';
import { spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { networkInterfaces, tmpdir } from 'node:os';
import { join } from 'node:path';
import { startHub, ADMIN_PASSWORD, HUB_BIN, type Hub } from './hub';

/**
 * Clipboard behaviour on a plain-HTTP home LAN.
 *
 * A Hub reached over `http://192.168.x.y` is **not** a secure context, so the
 * async Clipboard API may be absent and `execCommand('copy')` may be refused.
 * The UI must then offer a selectable-text fallback instead of throwing or
 * claiming the copy worked.
 *
 * Sandboxed CI machines also grant clipboard permissions inconsistently, so
 * these tests replace the clipboard surfaces themselves (via `addInitScript`)
 * rather than relying on the host's real permission state. The last test uses a
 * genuinely non-secure origin (a LAN IP) to prove the browser's own
 * `isSecureContext` really is false there.
 */

const PORT = Number(process.env.FLOWDROP_TEST_PORT ?? 8099);
const LAN_PORT = Number(process.env.FLOWDROP_LAN_TEST_PORT ?? 8096);

let hub: Hub;

test.beforeAll(async () => {
  hub = await startHub(PORT);
});

test.afterAll(async () => {
  await hub?.stop();
});

/** Collects uncaught page errors so no test tolerates a crash. */
function trackErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on('pageerror', (err) => errors.push(String(err)));
  return errors;
}

async function login(page: Page, device = 'clipboard-tester'): Promise<void> {
  await page.goto('/');
  await page.getByLabel('管理员密码').fill(ADMIN_PASSWORD);
  await page.getByRole('button', { name: '登录' }).click();
  await expect(page.getByRole('heading', { name: 'FlowDrop' })).toBeVisible();
  await page.getByLabel('本机名称').fill(device);
  await expect(page.getByText('实时同步中')).toBeVisible({ timeout: 15_000 });
}

async function sendText(page: Page, text: string): Promise<void> {
  await page.getByPlaceholder(/输入或粘贴文字/).fill(text);
  await page.getByRole('button', { name: '发送' }).click();
  await expect(page.locator('.item', { hasText: text }).first()).toBeVisible({ timeout: 10_000 });
}

test.describe('clipboard degradation', () => {
  test('no Clipboard API and refused execCommand exposes manual copy, not a false success', async ({
    browser,
  }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const errors = trackErrors(page);

    // Simulate a browser that exposes neither path. Setting these on the
    // instance shadows the prototype, which is what an insecure origin sees.
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        get: () => undefined,
      });
      Object.defineProperty(document, 'execCommand', {
        configurable: true,
        value: () => false,
      });
    });

    await login(page);
    const message = `手动复制用例 ${Date.now()}`;
    await sendText(page, message);

    const card = page.locator('.item', { hasText: message }).first();
    await card.getByRole('button', { name: '复制' }).click();

    // Success must NOT be claimed.
    await expect(page.getByText('已复制到剪贴板')).toHaveCount(0);
    await expect(page.getByText('无法自动复制，已展开文本供手动复制')).toBeVisible();

    // A pre-selected, read-only textarea carries the exact value.
    const manual = card.locator('.manual-copy');
    await expect(manual).toBeVisible();
    const textarea = manual.locator('textarea');
    await expect(textarea).toHaveValue(message);
    await expect(textarea).toHaveAttribute('readonly', '');
    await expect(manual).toContainText('手动选择');

    // The selection covers the whole value, so Ctrl/⌘+C alone is enough.
    const selection = await textarea.evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.select();
      return [el.selectionStart, el.selectionEnd, el.value.length];
    });
    expect(selection).toEqual([0, message.length, message.length]);

    // "全选" re-selects after focus moved elsewhere, and "收起" hides the panel.
    await page.getByRole('button', { name: '收起' }).click();
    await expect(card.locator('.manual-copy')).toHaveCount(0);

    expect(errors).toEqual([]);
    await ctx.close();
  });

  test('SHA-256 copy also degrades to selectable text for attachments', async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const errors = trackErrors(page);

    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        get: () => undefined,
      });
      Object.defineProperty(document, 'execCommand', {
        configurable: true,
        value: () => false,
      });
    });

    await login(page, 'hash-tester');
    const name = `hash-${Date.now()}.txt`;
    await page.setInputFiles('input[type=file]', {
      name,
      mimeType: 'text/plain',
      buffer: Buffer.from('checksum me'),
    });

    const card = page.locator('.item', { hasText: name }).first();
    await expect(card).toBeVisible({ timeout: 15_000 });

    // The hash is already on the button's title attribute.
    const hash = await card.getByRole('button', { name: '复制 SHA-256' }).getAttribute('title');
    expect(hash).toMatch(/^[0-9a-f]{64}$/);

    await card.getByRole('button', { name: '复制 SHA-256' }).click();
    const textarea = card.locator('.manual-copy textarea');
    await expect(textarea).toHaveValue(hash!);
    await expect(page.getByText('已复制到剪贴板')).toHaveCount(0);

    expect(errors).toEqual([]);
    await ctx.close();
  });

  test('denied clipboard permission falls back instead of reporting success', async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const errors = trackErrors(page);

    // Secure context stays as-is; the API exists but rejects as it would when
    // the user dismisses the permission prompt.
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          writeText: () =>
            Promise.reject(
              Object.assign(new Error('denied'), { name: 'NotAllowedError' }),
            ),
          readText: () => Promise.resolve(''),
        },
      });
      Object.defineProperty(document, 'execCommand', {
        configurable: true,
        value: () => false,
      });
    });

    await login(page, 'denied-tester');
    const message = `权限被拒 ${Date.now()}`;
    await sendText(page, message);

    const card = page.locator('.item', { hasText: message }).first();
    await card.getByRole('button', { name: '复制' }).click();

    const manual = card.locator('.manual-copy');
    await expect(manual).toBeVisible();
    await expect(manual.locator('textarea')).toHaveValue(message);
    await expect(manual).toContainText('拒绝');
    await expect(page.getByText('已复制到剪贴板')).toHaveCount(0);
    expect(errors).toEqual([]);
    await ctx.close();
  });

  test('a working Clipboard API still reports success and shows no fallback', async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const errors = trackErrors(page);

    // Stub a successful write so the assertion does not depend on OS clipboard
    // permissions on the machine running the test.
    await page.addInitScript(() => {
      const written: string[] = [];
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          writeText: (v: string) => {
            written.push(v);
            return Promise.resolve();
          },
          readText: () => Promise.resolve(written[written.length - 1] ?? ''),
        },
      });
      (window as unknown as { __written: string[] }).__written = written;
    });

    await login(page, 'ok-tester');
    const message = `正常复制 ${Date.now()}`;
    await sendText(page, message);

    const card = page.locator('.item', { hasText: message }).first();
    await card.getByRole('button', { name: '复制' }).click();
    await expect(page.getByText('已复制到剪贴板')).toBeVisible();
    await expect(card.locator('.manual-copy')).toHaveCount(0);

    const written = await page.evaluate(
      () => (window as unknown as { __written: string[] }).__written,
    );
    expect(written).toEqual([message]);
    expect(errors).toEqual([]);
    await ctx.close();
  });
});

/** First non-internal IPv4, or undefined when the host has no LAN address. */
function lanIPv4(): string | undefined {
  for (const addrs of Object.values(networkInterfaces())) {
    for (const a of addrs ?? []) {
      if (a.family === 'IPv4' && !a.internal) return a.address;
    }
  }
  return undefined;
}

interface LanHub {
  baseURL: string;
  stop: () => Promise<void>;
}

/**
 * Boots a Hub on a real LAN IP so the browser sees a non-secure origin
 * (`http://192.168.x.y`). Chrome treats only localhost as a secure context, so
 * this exercises the real insecure-context branch rather than a mocked one.
 */
async function startLanHub(ip: string, port: number, dataDir: string): Promise<LanHub> {
  const { spawnSync } = await import('node:child_process');
  const init = spawnSync(HUB_BIN, ['admin', 'init', '--password-stdin'], {
    env: { ...process.env, FLOWDROP_DATA_DIR: dataDir },
    input: `${ADMIN_PASSWORD}\n${ADMIN_PASSWORD}\n`,
    encoding: 'utf8',
  });
  if (init.status !== 0) throw new Error(`admin init failed: ${init.stderr}`);

  const child: ChildProcess = spawn(HUB_BIN, ['serve'], {
    env: {
      ...process.env,
      FLOWDROP_DATA_DIR: dataDir,
      FLOWDROP_LISTEN: `${ip}:${port}`,
      FLOWDROP_ALLOWED_HOSTS: ip,
      FLOWDROP_COOKIE_SECURE: 'false',
      FLOWDROP_LOGIN_LIMIT_PER_MINUTE: '50',
      FLOWDROP_LOG_LEVEL: 'warn',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  const baseURL = `http://${ip}:${port}`;
  const deadline = Date.now() + 20_000;
  for (;;) {
    try {
      const res = await fetch(`${baseURL}/api/v1/health`);
      if (res.ok) break;
    } catch {
      /* not up yet */
    }
    if (Date.now() > deadline) {
      child.kill('SIGKILL');
      throw new Error(`LAN hub did not start at ${baseURL}`);
    }
    await new Promise((r) => setTimeout(r, 200));
  }

  return {
    baseURL,
    stop: () =>
      new Promise<void>((resolve) => {
        if (child.exitCode !== null) {
          resolve();
          return;
        }
        const timer = setTimeout(() => {
          child.kill('SIGKILL');
          resolve();
        }, 5000);
        child.once('exit', () => {
          clearTimeout(timer);
          resolve();
        });
        child.kill('SIGTERM');
      }),
  };
}

test.describe('plain HTTP LAN origin', () => {
  test('non-secure LAN origin offers manual copy and never throws', async ({ browser }) => {
    const ip = lanIPv4();
    test.skip(!ip, 'host has no non-internal IPv4 address to serve the Hub on');

    const dataDir = mkdtempSync(join(tmpdir(), 'flowdrop-lan-'));
    let lan: LanHub | undefined;
    try {
      lan = await startLanHub(ip!, LAN_PORT, dataDir);

      const ctx = await browser.newContext();
      const page = await ctx.newPage();
      const errors = trackErrors(page);

      // Force both copy paths to fail; the point of this test is the *real*
      // non-secure origin, not this machine's clipboard permissions.
      await page.addInitScript(() => {
        Object.defineProperty(navigator, 'clipboard', {
          configurable: true,
          get: () => undefined,
        });
        Object.defineProperty(document, 'execCommand', {
          configurable: true,
          value: () => false,
        });
      });

      await page.goto(lan.baseURL + '/');
      // The browser itself confirms the plain-HTTP origin is not secure.
      expect(await page.evaluate(() => window.isSecureContext)).toBe(false);

      await page.getByLabel('管理员密码').fill(ADMIN_PASSWORD);
      await page.getByRole('button', { name: '登录' }).click();
      await expect(page.getByText('实时同步中')).toBeVisible({ timeout: 15_000 });

      // The one-time HTTP notice tells the user what may be degraded.
      await expect(page.getByText(/当前通过 HTTP 访问/)).toBeVisible();

      const message = `局域网明文 ${Date.now()}`;
      await sendText(page, message);
      const card = page.locator('.item', { hasText: message }).first();
      await card.getByRole('button', { name: '复制' }).click();

      await expect(card.locator('.manual-copy textarea')).toHaveValue(message);
      await expect(page.getByText('已复制到剪贴板')).toHaveCount(0);
      await expect(page.getByText(/HTTP 访问，浏览器未开放自动复制/)).toBeVisible();

      expect(errors).toEqual([]);
      await ctx.close();
    } finally {
      await lan?.stop();
      rmSync(dataDir, { recursive: true, force: true });
    }
  });
});
