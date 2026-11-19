import { test, expect, type Page } from '@playwright/test';
import { createPairingCode, startHub, type Hub } from './hub';
import { makePNG } from './fixtures';
import { createHash } from 'node:crypto';

const PORT = Number(process.env.FLOWDROP_TEST_PORT ?? 8099);

let hub: Hub;

test.beforeAll(async () => {
  hub = await startHub(PORT);
});

test.afterAll(async () => {
  await hub?.stop();
});

async function login(page: Page, device: string): Promise<void> {
  await page.goto('/');
  // Every browser context pairs with its own single-use code; a new code voids
  // the previous one, so contexts must pair one after another.
  await page.getByLabel('配对码').fill(createPairingCode(hub.dataDir));
  await page.getByRole('button', { name: '配对', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'FlowDrop' })).toBeVisible();
  // Every browser session identifies itself so the inbox can label the sender.
  const field = page.getByLabel('本机名称');
  await field.fill(device);
  await expect(page.getByText('实时同步中')).toBeVisible({ timeout: 15_000 });
}

test.describe('FlowDrop MVP end-to-end', () => {
  test('unpaired visitor sees only the pairing form', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByLabel('配对码')).toBeVisible();
    await expect(page.getByRole('button', { name: '配对', exact: true })).toBeVisible();
    // The retired password entry point must be gone from the page.
    await expect(page.locator('input[type=password]')).toHaveCount(0);
    await expect(page.locator('.composer')).toHaveCount(0);

    const res = await page.request.get('/api/v1/items');
    expect(res.status()).toBe(401);
  });

  test('a wrong pairing code is rejected and grants no session', async ({ page }) => {
    await page.goto('/');
    // A fresh code exists, so a different four-digit value is simply wrong.
    const current = createPairingCode(hub.dataDir);
    const wrong = current === '0000' ? '0001' : '0000';
    await page.getByLabel('配对码').fill(wrong);
    await page.getByRole('button', { name: '配对', exact: true }).click();

    await expect(page.getByRole('alert')).toContainText('配对码错误或已失效');
    await expect(page.locator('.composer')).toHaveCount(0);
    const res = await page.request.get('/api/v1/items');
    expect(res.status()).toBe(401);
  });

  test('text sent in one browser appears live in another', async ({ browser }) => {
    const a = await browser.newContext();
    const b = await browser.newContext();
    const pageA = await a.newPage();
    const pageB = await b.newPage();

    await login(pageA, 'Kubuntu 桌面');
    await login(pageB, 'Android 手机');

    const message = `来自 A 的消息 ${Date.now()}`;
    await pageA.getByPlaceholder(/输入或粘贴文字/).fill(message);
    await pageA.getByRole('button', { name: '发送' }).click();

    // B receives it over WebSocket, without reloading.
    await expect(pageB.getByText(message)).toBeVisible({ timeout: 10_000 });
    // The sender is labelled.
    await expect(pageB.getByText(/Kubuntu 桌面 ·/)).toBeVisible();

    // Copy path works (Chromium grants clipboard in secure contexts).
    await pageB.context().grantPermissions(['clipboard-read', 'clipboard-write'], {
      origin: hub.baseURL,
    });
    await pageB
      .locator('.item', { hasText: message })
      .getByRole('button', { name: '复制' })
      .click();
    await expect(pageB.getByText('已复制到剪贴板')).toBeVisible();
    const clip = await pageB.evaluate(() => navigator.clipboard.readText());
    expect(clip).toBe(message);

    await a.close();
    await b.close();
  });

  test('link is rendered as a link and never as HTML', async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await login(page, 'tester');

    const url = 'https://example.com/path?q=1';
    await page.getByPlaceholder(/输入或粘贴文字/).fill(url);
    await page.getByRole('button', { name: '发送' }).click();

    const card = page.locator('.item', { hasText: 'example.com' }).first();
    await expect(card.locator('a.link-url')).toHaveAttribute('href', url);
    await expect(card.locator('a.link-url')).toHaveAttribute('rel', /noopener/);
    await expect(card.locator('a.link-url')).toHaveAttribute('rel', /noreferrer/);

    // A text payload that looks like markup must be rendered literally.
    const markup = '<img src=x onerror=alert(1)>';
    await page.getByPlaceholder(/输入或粘贴文字/).fill(markup);
    await page.getByRole('button', { name: '发送' }).click();
    await expect(page.getByText(markup)).toBeVisible();
    await expect(page.locator('.item img[src="x"]')).toHaveCount(0);

    await ctx.close();
  });

  test('image upload shows a preview and downloads the original bytes', async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await login(page, 'phone');
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], {
      origin: hub.baseURL,
    });

    const png = makePNG(6, 6);
    const name = `screenshot-${Date.now()}.png`;
    await page.setInputFiles('.composer input[type=file]', {
      name,
      mimeType: 'image/png',
      buffer: png,
    });

    const card = page.locator('.item', { hasText: name }).first();
    await expect(card).toBeVisible({ timeout: 15_000 });
    await expect(card.locator('.tag-image')).toBeVisible();

    const img = card.locator('img');
    await expect(img).toBeVisible();
    // The <img> must actually decode, proving the authenticated preview works.
    await expect
      .poll(async () => img.evaluate((el: HTMLImageElement) => el.naturalWidth), {
        timeout: 10_000,
      })
      .toBe(6);

    // Full-size preview opens on click.
    await card.locator('.thumb').click();
    await expect(page.locator('.lightbox img')).toBeVisible();
    await page.locator('.lightbox').getByRole('button', { name: '关闭' }).click();

    // Download returns the identical bytes and SHA-256.
    const href = await card.getByRole('link', { name: '下载' }).getAttribute('href');
    expect(href).toBeTruthy();
    const res = await page.request.get(href!);
    expect(res.status()).toBe(200);
    const body = await res.body();
    expect(body.equals(png)).toBe(true);
    const sha = createHash('sha256').update(png).digest('hex');
    expect(res.headers()['x-content-sha256']).toBe(sha);
    expect(res.headers()['content-disposition']).toContain('attachment');

    await ctx.close();
  });

  test('APK upload is downloadable and delete removes it everywhere', async ({ browser }) => {
    const a = await browser.newContext();
    const b = await browser.newContext();
    const pageA = await a.newPage();
    const pageB = await b.newPage();
    await login(pageA, 'buildbox');
    await login(pageB, 'phone');

    const apk = Buffer.from('PK\x03\x04' + 'A'.repeat(4000));
    const name = `app-debug-${Date.now()}.apk`;
    await pageA.setInputFiles('.composer input[type=file]', {
      name,
      mimeType: 'application/vnd.android.package-archive',
      buffer: apk,
    });

    const cardA = pageA.locator('.item', { hasText: name }).first();
    await expect(cardA).toBeVisible({ timeout: 15_000 });
    await expect(cardA.locator('.tag-file')).toBeVisible();
    await expect(cardA.getByText('Android 安装包')).toBeVisible();

    // B sees it live and can download it.
    const cardB = pageB.locator('.item', { hasText: name }).first();
    await expect(cardB).toBeVisible({ timeout: 10_000 });
    const href = await cardB.getByRole('link', { name: '下载' }).getAttribute('href');
    const res = await pageB.request.get(href!);
    expect(res.status()).toBe(200);
    expect((await res.body()).equals(apk)).toBe(true);

    // A deletes; B's card disappears via WS without a reload.
    await cardA.getByRole('button', { name: '删除' }).click();
    await cardA.getByRole('button', { name: '确认删除' }).click();
    await expect(cardB).toHaveCount(0, { timeout: 10_000 });

    // The attachment is gone from the API too.
    const again = await pageB.request.get(href!);
    expect(again.status()).toBe(404);

    await a.close();
    await b.close();
  });

  test('multiple files upload sequentially, one item each', async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await login(page, 'multi');

    const stamp = Date.now();
    const files = [
      { name: `one-${stamp}.txt`, mimeType: 'text/plain', buffer: Buffer.from('first') },
      { name: `two-${stamp}.txt`, mimeType: 'text/plain', buffer: Buffer.from('second') },
      { name: `three-${stamp}.txt`, mimeType: 'text/plain', buffer: Buffer.from('third') },
    ];
    await page.setInputFiles('.composer input[type=file]', files);

    for (const f of files) {
      await expect(page.locator('.item', { hasText: f.name }).first()).toBeVisible({
        timeout: 20_000,
      });
    }
    for (const f of files) {
      await expect(page.locator('.item', { hasText: f.name })).toHaveCount(1);
    }
    await ctx.close();
  });

  test('oversize upload is rejected without creating an item', async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await login(page, 'oversize');

    // The test hub is configured with a 2 MiB per-file limit.
    const name = `huge-${Date.now()}.bin`;
    await page.setInputFiles('.composer input[type=file]', {
      name,
      mimeType: 'application/octet-stream',
      buffer: Buffer.alloc(3 * 1024 * 1024, 1),
    });

    // The client pre-checks the limit reported by the server and refuses to
    // start the upload, so either the pre-check message or a server-side
    // rejection is acceptable — what matters is that no item appears.
    await expect(page.getByText(/超过 .* 上限|上传失败/).first()).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator('.item', { hasText: name })).toHaveCount(0);

    // The client pre-check is a convenience, not the security boundary. Bypass
    // it and confirm the server itself rejects the oversized body.
    const serverStatus = await page.evaluate(async () => {
      const csrf = document.cookie.match(/(?:^|;\s*)flowdrop_csrf=([^;]+)/)?.[1] ?? '';
      const form = new FormData();
      form.append(
        'file',
        new File([new Uint8Array(3 * 1024 * 1024)], 'bypass.bin', {
          type: 'application/octet-stream',
        }),
      );
      const res = await fetch('/api/v1/items/upload', {
        method: 'POST',
        body: form,
        headers: { 'X-FlowDrop-CSRF': decodeURIComponent(csrf) },
        credentials: 'same-origin',
      });
      return res.status;
    });
    expect(serverStatus).toBe(413);
    await ctx.close();
  });

  test('reconnect after offline resyncs deletions made while disconnected', async ({
    browser,
  }) => {
    const ctxA = await browser.newContext();
    const pageA = await ctxA.newPage();
    await login(pageA, 'producer');

    const marker = `ephemeral-${Date.now()}`;
    await pageA.getByPlaceholder(/输入或粘贴文字/).fill(marker);
    await pageA.getByRole('button', { name: '发送' }).click();
    await expect(pageA.locator('.item', { hasText: marker }).first()).toBeVisible();

    const ctxB = await browser.newContext();
    const pageB = await ctxB.newPage();
    await login(pageB, 'consumer');
    await expect(pageB.locator('.item', { hasText: marker }).first()).toBeVisible();

    // B goes offline (WS + REST both fail), A deletes, then B comes back.
    await ctxB.setOffline(true);
    const cardA = pageA.locator('.item', { hasText: marker }).first();
    await cardA.getByRole('button', { name: '删除' }).click();
    await cardA.getByRole('button', { name: '确认删除' }).click();
    await expect(cardA).toHaveCount(0);

    await ctxB.setOffline(false);
    // Resync on online/focus must drop the deleted item and reset pagination.
    await expect(pageB.locator('.item', { hasText: marker })).toHaveCount(0, {
      timeout: 20_000,
    });
    await expect(pageB.getByText('实时同步中')).toBeVisible({ timeout: 20_000 });

    await ctxA.close();
    await ctxB.close();
  });

  test('pairing survives a page reload; unpairing revokes it', async ({ browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await login(page, 'reload');
    // A reload reuses the HttpOnly cookie instead of asking for a new code.
    await page.reload();
    await expect(page.getByPlaceholder(/输入或粘贴文字/)).toBeVisible();
    await expect(page.getByLabel('配对码')).toHaveCount(0);
    await page.getByRole('button', { name: '解除配对' }).click();
    await expect(page.getByLabel('配对码')).toBeVisible();

    // The old cookie is useless after unpairing.
    const res = await page.request.get('/api/v1/items');
    expect(res.status()).toBe(401);
    await ctx.close();
  });
});
