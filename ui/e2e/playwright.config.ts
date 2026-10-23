import { defineConfig, devices } from '@playwright/test';

const baseURL = 'http://127.0.0.1:18080';

export default defineConfig({
  testDir: './tests',
  // The fake backend holds one shared in-memory state, so specs run serially.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    // cwd is the repo root so the Go module resolves.
    command: 'go run ./ui/e2e/fakeserver -addr 127.0.0.1:18080',
    cwd: '../..',
    url: `${baseURL}/api/tasks`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
