import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  fullyParallel: false,
  // Several specs change global settings (AI gateway, SMTP relay) that the
  // whole server shares, so files run one at a time rather than in parallel
  // workers overwriting each other's configuration.
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: process.env.KANPIC_E2E_BASE_URL || 'http://localhost:8080',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } : undefined,
  },
})
