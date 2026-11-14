import { defineConfig, devices } from '@playwright/test';

// Browser checks against a running panel (the dev compose by default):
//   ADMIN_USER=admin ADMIN_PASSWORD_FILE=./pw npm run test:e2e
const env = (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env ?? {};
const storageState = 'e2e/.auth/admin.json';

export default defineConfig({
	testDir: 'e2e',
	timeout: 30_000,
	workers: 1,
	use: { baseURL: env.BASE_URL ?? 'http://localhost:8090', trace: 'retain-on-failure' },
	projects: [
		{ name: 'setup', testMatch: /auth\.setup\.ts/ },
		{ name: 'desktop', dependencies: ['setup'], use: { ...devices['Desktop Chrome'], storageState } },
		{ name: 'mobile', dependencies: ['setup'], use: { ...devices['Pixel 7'], storageState } }
	]
});
