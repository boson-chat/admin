import { expect, test, type Page } from '@playwright/test';

// The Nick registration page against the panel running on the fake auth
// and the fake api internal listener (e2e/fake-api.mjs), which reads the
// nick_claim: section back from the config file:
//
//   ./scripts/e2e-nick.sh     # starts all three; SCREENSHOT_DIR=... keeps screenshots
const fake = process.env.FAKE_API_URL ?? '';
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

test.skip(!fake, 'set FAKE_API_URL (the panel must run on e2e/fake-api.mjs; scripts/e2e-nick.sh)');

const KEYS = [
	'nick_claim.enabled',
	'nick_claim.email_domain',
	'nick_claim.rate_limit_per_hour',
	'nick_claim.inbound.protocol',
	'nick_claim.inbound.host',
	'nick_claim.inbound.port',
	'nick_claim.inbound.tls',
	'nick_claim.inbound.user'
];

async function apiState(page: Page, s: Record<string, unknown>) {
	const r = await page.request.post(`${fake}/__state`, { data: s });
	expect(r.ok()).toBe(true);
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

async function open(page: Page) {
	await page.goto('/nick-registration');
	await expect(page.getByRole('heading', { name: 'Nick registration', level: 1 })).toBeVisible();
	await expect(page.getByRole('textbox', { name: 'Mail server' })).toBeVisible();
}

async function noHorizontalOverflow(page: Page) {
	const [scroll, width] = await page.evaluate(() => [
		document.documentElement.scrollWidth,
		document.documentElement.clientWidth
	]);
	expect(scroll).toBeLessThanOrEqual(width);
}

test.beforeEach(async ({ page }) => {
	await apiState(page, { reset: true });
	await patchConfig(page, { unset: KEYS });
});

test('blocks enabling without a domain or mail server', async ({ page }) => {
	await open(page);
	// The env gives a mail server; clear it to see both errors.
	await page.getByRole('textbox', { name: 'Mail server' }).fill('');
	await page.getByRole('switch', { name: /Register nicks on other networks/ }).check({ force: true });
	await expect(page.getByText('Needed to turn nick registration on.')).toHaveCount(2);
	await expect(page.locator('p[role="alert"]').filter({ hasText: 'below to turn it on' })).toContainText(
		'an email domain and a mail server'
	);
	await expect(page.getByRole('button', { name: 'Save changes' })).toBeDisabled();
	await expect(page.getByRole('textbox', { name: 'Email domain' })).toHaveAttribute('aria-invalid', 'true');
});

test('tests unsaved values, saves, and the api picks it up', async ({ page }) => {
	await open(page);
	await expect(page.getByTestId('password-state')).toContainText('Set in .env');
	await expect(page.getByTestId('worker-state')).toContainText('Off');
	await expect(page.getByTestId('dedicated-warning')).toContainText('Use a mailbox dedicated to this');

	// The server in use: the api's stored password works.
	await page.getByRole('button', { name: 'Test connection' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Connection works.' })).toContainText(
		'3 message(s) waiting'
	);

	// Another server: it asks for a password for this test only.
	const host = page.getByRole('textbox', { name: 'Mail server' });
	await host.fill('mail.example.net');
	const pw = page.getByLabel('Password for this test');
	await expect(pw).toBeVisible();
	await page.getByRole('button', { name: 'Test connection' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Test failed.' })).toContainText('invalid login');
	await pw.fill('mailbox-password');
	await page.getByRole('button', { name: 'Test connection' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Connection works.' })).toContainText(
		'mail.example.net'
	);

	// Nothing saved by testing.
	const before = await (
		await page.request.get(`${fake}/internal/nick-claims/status`, {
			headers: { 'X-Internal-Token': 'e2e-api' }
		})
	).json();
	expect(before.inbound.host).toBe('pop.env.example.com');

	await page.getByRole('switch', { name: /Register nicks on other networks/ }).check({ force: true });
	await page.getByRole('textbox', { name: 'Email domain' }).fill('nick.example.com');
	await expect(page.getByText('reg+<user id>-<token>@nick.example.com')).toBeVisible();
	await page.getByRole('textbox', { name: 'Username' }).fill('reg@nick.example.com');
	await page.getByRole('button', { name: 'Save changes' }).click();
	await expect(page.getByText(/Nick registration saved/)).toBeVisible();

	// The api's config status (after a save) and its worker.
	await expect(page.locator('li[data-state="applied"]').filter({ hasText: 'API' })).toBeVisible({
		timeout: 10_000
	});
	await expect(page.getByTestId('worker-state')).toContainText('Running', { timeout: 10_000 });

	const v = await page.evaluate(async () => (await fetch('/api/config')).json());
	const val = (k: string) => v.keys.find((x: { key: string }) => x.key === k)?.value;
	expect(val('nick_claim.enabled')).toBe(true);
	expect(val('nick_claim.email_domain')).toBe('nick.example.com');
	expect(val('nick_claim.inbound.host')).toBe('mail.example.net');
	expect(val('nick_claim.inbound.protocol')).toBe('pop3');
	expect(JSON.stringify(v)).not.toContain('mailbox-password');
});

test('shows a failing worker and a missing password', async ({ page }) => {
	await apiState(page, {
		running: true,
		password_set: false,
		consecutive_failures: 4,
		last_error: 'sign-in failed: -ERR [AUTH] invalid login'
	});
	await open(page);
	await expect(page.getByTestId('worker-state')).toContainText('Failing');
	await expect(page.getByTestId('password-state')).toContainText('Missing');
	await expect(page.getByText(/docker compose up -d api/)).toBeVisible();
	await expect(page.locator('dd.last-err')).toContainText('invalid login');
});

test('says when the api refuses the panel’s token', async ({ page }) => {
	await apiState(page, { token_ok: false });
	await open(page);
	await expect(page.getByText(/Couldn’t read the api’s status/)).toContainText('BOSON_API_INTERNAL_TOKEN');
});

for (const mode of ['light', 'dark'] as const) {
	test(`screenshot (${mode})`, async ({ page }, info) => {
		await patchConfig(page, {
			set: {
				'nick_claim.enabled': true,
				'nick_claim.email_domain': 'nick.example.com',
				'nick_claim.inbound.protocol': 'pop3',
				'nick_claim.inbound.host': 'pop.env.example.com',
				'nick_claim.inbound.user': 'reg@nick.example.com'
			}
		});
		await apiState(page, { pending_claims: 3 });
		await page.addInitScript((m) => localStorage.setItem('theme', m), mode);
		await open(page);
		await expect(page.getByTestId('worker-state')).toContainText('Running');
		await page.getByRole('button', { name: 'Test connection' }).click();
		await expect(page.getByRole('status').filter({ hasText: 'Connection works.' })).toBeVisible();
		await noHorizontalOverflow(page);
		await page.waitForTimeout(200);
		const vp = page.viewportSize()!;
		const h = await page.evaluate(() => document.documentElement.scrollHeight);
		await page.setViewportSize({ width: vp.width, height: Math.max(vp.height, h) });
		await page.waitForTimeout(200);
		await page.screenshot({ path: `${dir}/nick-registration-${info.project.name}-${mode}.png` });
		await page.setViewportSize(vp);
	});
}

test('screenshot of the blocked state', async ({ page }, info) => {
	await page.addInitScript(() => localStorage.setItem('theme', 'dark'));
	await open(page);
	await page.getByRole('textbox', { name: 'Mail server' }).fill('');
	await page.getByRole('switch', { name: /Register nicks on other networks/ }).check({ force: true });
	await noHorizontalOverflow(page);
	await page.screenshot({
		path: `${dir}/nick-registration-blocked-${info.project.name}-dark.png`,
		fullPage: true
	});
});
