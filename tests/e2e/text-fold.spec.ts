import { test, expect, type Page } from '@playwright/test';
import { createPairingCode, startHub, type Hub } from './hub';

const PORT = Number(process.env.FLOWDROP_TEST_PORT ?? 8099);

// Two long URLs with a blank line between them: 269 characters, which the old
// character estimate treated as 9 lines and folded even when the wide desktop
// column rendered only 7 — the toggle then revealed a single line of URL tail
// and read as broken.
const TWO_URLS = [
  '银驰 GTX1660 v 友 820r',
  'https://www.goofish.com/item?spm=a21ybx.personal.feeds.1.37447cdeNE1KeA&id=1099181190030&categoryId=50025387',
  '',
  '威刚 DDR4 2666 内存条 8g*3 v 友 680r',
  'https://www.goofish.com/item?spm=a21ybx.personal.feeds.2.37447cdeNE1KeA&id=1101369016573&categoryId=50025387',
  '',
].join('\n');

const LONG_TEXT = Array.from(
  { length: 40 },
  (_, i) => `第 ${i + 1} 行：这是一段很长很长的正文内容用于验证折叠。`,
).join('\n');

let hub: Hub;

test.beforeAll(async () => {
  hub = await startHub(PORT);
});

test.afterAll(async () => {
  await hub?.stop();
});

async function login(page: Page): Promise<void> {
  await page.goto('/');
  await page.getByLabel('配对码').fill(createPairingCode(hub.dataDir));
  await page.getByRole('button', { name: '配对', exact: true }).click();
  await page.getByLabel('本机名称').fill('文本折叠');
  await expect(page.getByText('实时同步中')).toBeVisible({ timeout: 15_000 });
}

async function send(page: Page, text: string): Promise<void> {
  await page.getByPlaceholder(/输入或粘贴文字/).fill(text);
  await page.getByRole('button', { name: '发送' }).click();
  await expect(page.locator('.item-text .text-body').first()).toBeVisible();
}

/** The item just sent (the inbox lists newest first). */
function newest(page: Page) {
  return page.locator('li.item-text').first();
}

/** Rendered height in lines of the newest text item. */
async function lines(page: Page): Promise<number> {
  return newest(page)
    .locator('.text-body')
    .evaluate((el) =>
      Math.round(
        el.getBoundingClientRect().height / (parseFloat(getComputedStyle(el).lineHeight) || 20),
      ),
    );
}

test.describe('text folding follows the rendered height', () => {
  test('a wide window does not fold text it can show in full', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await login(page);
    await send(page, TWO_URLS);
    // Nothing is hidden, so there is nothing to expand: no toggle, all 7 lines.
    await expect(newest(page).locator('.text-toggle')).toHaveCount(0);
    expect(await lines(page)).toBe(7);
  });

  test('the same text folds and unfolds on a narrow window', async ({ page }) => {
    await page.setViewportSize({ width: 420, height: 900 });
    await login(page);
    await send(page, TWO_URLS);
    const toggle = newest(page).locator('.text-toggle');
    await expect(toggle).toHaveText('展开全文');
    expect(await lines(page)).toBe(6);

    await toggle.click();
    await expect(toggle).toHaveText('收起');
    // Unfolding reveals several lines here, so the toggle is useful.
    expect(await lines(page)).toBeGreaterThan(7);

    await toggle.click();
    await expect(toggle).toHaveText('展开全文');
    expect(await lines(page)).toBe(6);
  });

  test('a long message still folds and unfolds in a wide window', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await login(page);
    await send(page, LONG_TEXT);
    const toggle = newest(page).locator('.text-toggle');
    await expect(toggle).toHaveText('展开全文');
    expect(await lines(page)).toBe(6);

    await toggle.click();
    await expect(toggle).toHaveText('收起');
    expect(await lines(page)).toBe(40);
  });

  test('resizing the window re-decides the fold', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await login(page);
    await send(page, TWO_URLS);
    await expect(newest(page).locator('.text-toggle')).toHaveCount(0);

    // Narrowing wraps the text past the budget: the toggle must appear.
    await page.setViewportSize({ width: 420, height: 900 });
    await expect(newest(page).locator('.text-toggle')).toHaveText('展开全文');
    expect(await lines(page)).toBe(6);

    // Widening again shows everything, so the toggle goes away by itself.
    await page.setViewportSize({ width: 1280, height: 900 });
    await expect(newest(page).locator('.text-toggle')).toHaveCount(0);
    expect(await lines(page)).toBe(7);
  });
});
