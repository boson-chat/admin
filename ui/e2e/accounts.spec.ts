import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';

// Auth without SMTP (email.configured false) and NickServ accounts, against
// the panel on the fake auth with roles and a config file:
//
//   ./scripts/e2e-accounts.sh  # SCREENSHOT_DIR=... keeps screenshots
const fake = process.env.FAKE_AUTH_INTERNAL_URL ?? '';
const configFile = process.env.BOSON_CONFIG_FILE ?? '';
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

test.skip(!fake, 'set FAKE_AUTH_INTERNAL_URL (scripts/e2e-accounts.sh)');

async function accounts(page: Page, data: { email: boolean; backend: string }) {
	expect((await page.request.post(`${fake}/__accounts`, { data })).ok()).toBe(true);
}

async function shoot(page: Page, name: string, project: string, focus?: string) {
	const [scroll, width] = await page.evaluate(() => [
		document.documentElement.scrollWidth,
		document.documentElement.clientWidth
	]);
	expect(scroll).toBeLessThanOrEqual(width);
	await page.waitForTimeout(200);
	if (focus) await page.getByTestId(focus).screenshot({ path: `${dir}/${name}-${project}-part.png` });
	await page.screenshot({ path: `${dir}/${name}-${project}.png`, fullPage: true });
}

/** Calls the BFF from the page (same cookie and CSRF token as the UI). */
async function patchConfig(page: Page, body: object) {
	await page.goto('/');
	const status = await page.evaluate(async (body) => {
		const s = await (await fetch('/api/session')).json();
		const r = await fetch('/api/config', {
			method: 'PATCH',
			headers: {
				'X-CSRF-Token': s.csrf_token,
				'X-Requested-With': 'e2e',
				'Content-Type': 'application/json'
			},
			body: JSON.stringify(body)
		});
		return r.status;
	}, body);
	expect(status).toBe(200);
}

test.afterAll(async ({ request }) => {
	await request.post(`${fake}/__accounts`, { data: { email: true, backend: 'local' } });
});

test('email not configured: settings, emails and users explain it and hand out links', async ({
	page
}, info) => {
	await accounts(page, { email: false, backend: 'local' });
	await page.goto('/settings');
	const off = page.getByTestId('email-off');
	await expect(off).toContainText('Email isn’t set up');
	await expect(off).toContainText('No password reset by email');
	await expect(off).toContainText('stays saved and applies once email is set up');
	await expect(page.getByTestId('nickserv-signup')).toHaveCount(0);
	await shoot(page, 'settings-email-off', info.project.name, 'email-off');

	await page.goto('/emails');
	await expect(page.getByTestId('email-off')).toContainText('No email verification');
	await expect(page.getByRole('button', { name: 'Send test' })).toBeDisabled();
	await shoot(page, 'emails-email-off', info.project.name);

	await page.goto('/users/usr_maya');
	await page.getByRole('button', { name: 'Password reset link' }).click();
	await page.getByRole('button', { name: 'Make reset link' }).click();
	const dlg = page.getByTestId('reset-link');
	await expect(dlg).toBeVisible();
	await expect(dlg.getByRole('textbox')).toHaveValue('https://chat.example.org/reset#token=e2e-reset-token');
	await shoot(page, 'user-reset-link', info.project.name);
	await dlg.getByRole('button', { name: 'Done' }).click();

	await page.goto('/users');
	await page
		.getByRole('button', { name: /create user/i })
		.first()
		.click();
	const form = page.locator('dialog[open]');
	await form.getByLabel('Username').fill('nina');
	await form.getByLabel('Email', { exact: true }).fill('nina@example.com');
	await expect(page.getByText('Make a set-password link')).toBeVisible();
	await form.getByRole('button', { name: 'Create user', exact: true }).click();
	const setup = page.getByTestId('setup-link');
	await expect(setup.getByRole('textbox')).toHaveValue(
		'https://chat.example.org/reset#token=e2e-setup-token'
	);
	await shoot(page, 'create-user-link', info.project.name);
});

test('email configured: no warnings', async ({ page }) => {
	await accounts(page, { email: true, backend: 'local' });
	await page.goto('/settings');
	await expect(page.getByRole('heading', { name: 'Network settings', level: 1 })).toBeVisible();
	await page.waitForTimeout(300);
	await expect(page.getByTestId('email-off')).toHaveCount(0);
	await page.goto('/emails');
	await expect(page.getByRole('heading', { name: 'Emails', level: 1 })).toBeVisible();
	await page.waitForTimeout(300);
	await expect(page.getByTestId('email-off')).toHaveCount(0);
});

test('NickServ accounts: the sign-up switch and message save to the file', async ({ page }, info) => {
	await patchConfig(page, { unset: ['auth.nickserv.registration', 'auth.nickserv.signup_message'] });
	await accounts(page, { email: false, backend: 'nickserv' });
	await page.goto('/settings');
	const ns = page.getByTestId('nickserv-signup');
	await expect(ns).toBeVisible();
	// NickServ handles its own email: no local email warning on this page.
	await expect(page.getByTestId('email-off')).toHaveCount(0);
	const sw = ns.getByRole('switch', { name: /Register NickServ accounts/ });
	await expect(sw).toBeChecked();
	await expect(ns.getByLabel('Sign-up message')).toHaveCount(0);
	await ns.getByText('Register NickServ accounts when people sign up on Boson').click();
	await expect(sw).not.toBeChecked();
	const msg = ns.getByLabel('Sign-up message');
	await msg.fill('Register on IRC: /msg NickServ REGISTER <password> <email>, then sign in here.');
	await shoot(page, 'settings-nickserv', info.project.name, 'nickserv-signup');
	await page.getByRole('button', { name: /save/i }).last().click();
	await expect(page.getByText('Network settings saved.')).toBeVisible();
	if (configFile) {
		const y = readFileSync(configFile, 'utf8');
		expect(y).toMatch(/nickserv:\s*\n\s+registration: false/);
		expect(y).toContain(
			"signup_message: 'Register on IRC: /msg NickServ REGISTER <password> <email>, then sign in here.'"
		);
	}
	// The Emails page says NickServ isn't affected.
	await page.goto('/emails');
	await expect(page.getByTestId('email-off')).toContainText('NickServ accounts verify');
	await patchConfig(page, { unset: ['auth.nickserv.registration', 'auth.nickserv.signup_message'] });
});
