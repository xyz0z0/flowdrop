import { test, expect } from '@playwright/test';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createPairingCode, startHub, type Hub } from './hub';

const PORT = Number(process.env.FLOWDROP_TEST_PORT ?? 8098);

/**
 * Proves content and sessions live in SQLite + disk, not in process memory:
 * a Hub restart must preserve items, attachments and the logged-in session.
 */
test('content and session survive a Hub restart', async ({ browser, page }) => {
  const dataDir = mkdtempSync(join(tmpdir(), 'flowdrop-restart-'));
  let hub: Hub | undefined;

  try {
    hub = await startHub(PORT, { dataDir });

    // This spec boots its own Hub on a different port than the shared baseURL.
    await page.goto(hub.baseURL + '/');
    // Pairing is per browser context, so this spec asks the local CLI for its
    // own one-time code instead of relying on a shared one.
    await page.getByLabel('配对码').fill(createPairingCode(dataDir));
    await page.getByRole('button', { name: '配对', exact: true }).click();
    await expect(page.getByText('实时同步中')).toBeVisible({ timeout: 15_000 });

    const stamp = Date.now();
    const text = `重启前写入 ${stamp}`;
    await page.getByPlaceholder(/输入或粘贴文字/).fill(text);
    await page.getByRole('button', { name: '发送' }).click();
    await expect(page.locator('.item', { hasText: text })).toHaveCount(1);

    const fileName = `persisted-${stamp}.txt`;
    await page.setInputFiles('.composer input[type=file]', {
      name: fileName,
      mimeType: 'text/plain',
      buffer: Buffer.from('survives restart'),
    });
    const card = page.locator('.item', { hasText: fileName }).first();
    await expect(card).toBeVisible({ timeout: 15_000 });
    const href = await card.getByRole('link', { name: '下载' }).getAttribute('href');
    expect(href).toBeTruthy();

    // Restart the hub on the same data dir.
    await hub.stop();
    hub = await startHub(PORT, { dataDir });

    // The restart must not revoke the paired device: the same data dir still
    // holds the cookie session row, so a reload stays inside the inbox.
    await page.reload();
    await expect(page.getByPlaceholder(/输入或粘贴文字/)).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('.item', { hasText: text })).toHaveCount(1);
    await expect(page.locator('.item', { hasText: fileName })).toHaveCount(1);

    // page.request resolves relative URLs against the config baseURL, so build
    // an absolute URL for this spec's own hub port.
    const res = await page.request.get(hub.baseURL + href!);
    expect(res.status()).toBe(200);
    expect((await res.text()).length).toBeGreaterThan(0);
  } finally {
    await hub?.stop();
    rmSync(dataDir, { recursive: true, force: true });
  }
});
