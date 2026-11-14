import { readFileSync } from 'node:fs';
import { expect, test as setup } from '@playwright/test';

// Signs in once (the auth service rate-limits logins) and saves the cookie
// for the other tests.
const env = process.env;
const user = env.ADMIN_USER ?? 'admin';
const password =
	env.ADMIN_PASSWORD ?? (env.ADMIN_PASSWORD_FILE ? readFileSync(env.ADMIN_PASSWORD_FILE, 'utf8').trim() : '');

const STATE = 'e2e/.auth/admin.json';

setup('sign in', async ({ page }) => {
	setup.skip(!password, 'set ADMIN_PASSWORD or ADMIN_PASSWORD_FILE');
	await page.goto('/login');
	await page.getByLabel(/username or email/i).fill(user);
	await page.getByLabel(/password/i).fill(password);
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page.getByRole('heading', { name: 'Stack', level: 1 })).toBeVisible();
	await page.context().storageState({ path: STATE });
});
