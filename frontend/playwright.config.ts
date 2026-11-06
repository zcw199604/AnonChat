import { defineConfig, devices } from '@playwright/test'

/** E2E 数据库（独立于 dev/test）。 */
export const E2E_DATABASE_URL =
  process.env.E2E_DATABASE_URL ??
  'postgres://anonchat:anonchat@127.0.0.1:55432/anonchat_e2e?sslmode=disable'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  globalSetup: './e2e/global-setup.ts',
  use: {
    baseURL: 'http://localhost:5173',
    trace: 'off',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: 'cd ../backend && go run ./cmd/server serve',
      url: 'http://127.0.0.1:8080/healthz',
      timeout: 180_000,
      reuseExistingServer: false,
      env: {
        APP_ENV: 'test',
        HTTP_ADDR: '127.0.0.1:8080',
        DATABASE_URL: E2E_DATABASE_URL,
        VERIFICATION_MODE: 'off',
        ALLOWED_ORIGINS: 'http://localhost:5173',
        RATE_LIMIT_MULTIPLIER: '100',
      },
    },
    {
      command: 'pnpm dev --port 5173 --strictPort',
      url: 'http://localhost:5173',
      timeout: 120_000,
      reuseExistingServer: false,
    },
  ],
})
