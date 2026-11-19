import { defineConfig, devices } from '@playwright/test';

const PORT = Number(process.env.FLOWDROP_TEST_PORT ?? 8099);
const BASE = `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: BASE,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
    // Local test origins are http://127.0.0.1, which browsers treat as a
    // secure context, so cookies/clipboard behave like production HTTPS.
    ignoreHTTPSErrors: process.env.FLOWDROP_TEST_HTTPS === '1',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        channel: process.env.PW_CHANNEL ?? undefined,
        launchOptions: process.env.PW_CHANNEL
          ? {}
          : { executablePath: process.env.CHROME_PATH || '/usr/bin/google-chrome' },
      },
    },
  ],
});
