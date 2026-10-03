import { defineConfig } from '@playwright/test'
import process from 'node:process'

const authState = 'test-results/.auth/admin.json'

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  reporter: 'list',
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'https://127.0.0.1',
    colorScheme: 'dark',
    ignoreHTTPSErrors: true,
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'auth-setup',
      testMatch: /auth\.setup\.ts/,
      teardown: 'auth-cleanup',
    },
    {
      name: 'ui-fixtures',
      testIgnore: [/auth\.setup\.ts/, /auth\.cleanup\.ts/, /pulse\.spec\.ts/],
    },
    {
      name: 'integration',
      testMatch: /pulse\.spec\.ts/,
      dependencies: ['auth-setup'],
      use: { storageState: authState },
    },
    {
      name: 'auth-cleanup',
      testMatch: /auth\.cleanup\.ts/,
      use: { storageState: authState },
    },
  ],
})
