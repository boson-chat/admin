import { readFileSync, statSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';

// The Settings page's Usage & health stats card against the panel sending to
// a fake receiver (ui/e2e/fake-telemetry.mjs): ./scripts/e2e-telemetry.sh
// (SCREENSHOT_DIR=... keeps screenshots).
const fake = process.env.FAKE_TELEMETRY_URL ?? '';
const configFile = process.env.BOSON_CONFIG_FILE ?? '';
const dataDir = process.env.TELEMETRY_DIR ?? '';
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

test.skip(!fake, 'set FAKE_TELEMETRY_URL (the panel must send there; scripts/e2e-telemetry.sh)');
test.describe.configure({ mode: 'serial' });

type Sent = { content_type: string; body: Record<string, unknown> };
async function reports(page: Page): Promise<Sent[]> {
	return (await (await page.request.get(`${fake}/__reports`)).json()).reports;
}

async function noHorizontalOverflow(page: Page) {
	const [scroll, width] = await page.evaluate(() => [
		document.documentElement.scrollWidth,
		document.documentElement.clientWidth
	]);
	expect(scroll).toBeLessThanOrEqual(width);
}

const widths = { desktop: 1280, phone: 390 } as const;

async function card(page: Page) {
	const c = page.getByTestId('usage-stats');
	await c.scrollIntoViewIfNeeded();
	return c;
}

test('the first report went out, valid, with nothing identifying in it', async ({ page }) => {
	await expect.poll(async () => (await reports(page)).length, { timeout: 15_000 }).toBeGreaterThan(0);
	const [first] = await reports(page);
	expect(first.content_type).toBe('application/json');
	const r = first.body;
	expect(r.schema).toBe(1);
	expect(r.reason).toBe('setup');
	expect(r.install_id).toMatch(/^[0-9a-f]{32}$/);
	expect(Object.keys(r).sort()).toEqual(
		[
			'counts',
			'features',
			'health',
			'host_size',
			'install',
			'install_id',
			'profiles',
			'reason',
			'schema',
			'services',
			'settings',
			'stack',
			'uptime_days'
		].sort()
	);
	// host_size: buckets (or null) from the proxy's GET /info, never exact figures.
	const hs = r.host_size as Record<string, string | null> | null;
	if (hs) {
		expect(Object.keys(hs).sort()).toEqual(['arch', 'cpu_cores', 'disk_gb', 'ram_gb']);
		expect([null, '1', '2', '3-4', '5-8', '9-16', '17-32', '33+']).toContain(hs.cpu_cores);
		expect([null, 'amd64', 'arm64', 'other']).toContain(hs.arch);
	}
	const text = JSON.stringify(r);
	for (const s of ['Example Chat', 'localhost', '127.0.0.1', 'admin@', 'e2e']) expect(text).not.toContain(s);
	// The ID is kept in the data dir, readable by the panel only.
	if (dataDir) {
		expect(readFileSync(`${dataDir}/telemetry-id`, 'utf8').trim()).toBe(r.install_id);
		expect(statSync(`${dataDir}/telemetry-id`).mode & 0o777).toBe(0o600);
	}
});

test('the card: why first, last result, what is sent', async ({ page }, info) => {
	await page.goto('/settings');
	const c = await card(page);
	await expect(c.getByRole('heading', { name: 'Usage & health stats' })).toBeVisible();
	await expect(c.getByText(/^We collect a small amount of anonymous usage and health data/)).toBeVisible();
	const sw = c.getByRole('switch', { name: /Send usage & health stats/ });
	await expect(sw).toBeChecked();
	await expect(sw).toBeEnabled();
	await expect(c.getByTestId('usage-result')).toContainText('Received');
	await expect(c.getByRole('link', { name: /What’s collected/ })).toHaveAttribute(
		'href',
		'https://boson.chat/telemetry'
	);

	if (info.project.name === 'desktop') {
		for (const [name, width] of Object.entries(widths)) {
			await page.setViewportSize({ width, height: name === 'phone' ? 844 : 900 });
			await page.goto('/settings');
			await (await card(page)).getByTestId('usage-result').waitFor();
			await page.waitForTimeout(300);
			await noHorizontalOverflow(page);
			await (await card(page)).screenshot({ path: `${dir}/admin-settings-${name}.png` });
			await page.getByRole('button', { name: 'See exactly what’s sent' }).click();
			const pre = page.getByTestId('usage-preview');
			await expect(pre).toBeVisible();
			await page.waitForTimeout(300);
			await noHorizontalOverflow(page);
			await page.screenshot({ path: `${dir}/admin-whats-sent-${name}.png` });
			await page.getByRole('button', { name: 'Done' }).click();
		}
	}

	await page.getByRole('button', { name: 'See exactly what’s sent' }).click();
	const pre = page.getByTestId('usage-preview');
	const preview = JSON.parse((await pre.textContent()) ?? '{}');
	const [first] = await reports(page);
	expect(preview.install_id).toBe(first.body.install_id);
	expect(preview.schema).toBe(1);
	await expect(page.getByRole('dialog')).toContainText(preview.install_id);
	await page.getByRole('button', { name: 'Done' }).click();
});

test('the switch writes admin.telemetry and back', async ({ page }) => {
	await page.goto('/settings');
	const c = await card(page);
	const sw = c.getByRole('switch', { name: /Send usage & health stats/ });
	await expect(sw).toBeChecked();
	await sw.click({ force: true });
	await expect(sw).not.toBeChecked();
	await expect(c.getByText('Not sending')).toBeVisible();
	if (configFile) expect(readFileSync(configFile, 'utf8')).toMatch(/telemetry: false/);
	await page.reload();
	await expect((await card(page)).getByRole('switch', { name: /Send usage/ })).not.toBeChecked();
	await (await card(page)).getByRole('switch', { name: /Send usage/ }).click({ force: true });
	await expect((await card(page)).getByRole('switch', { name: /Send usage/ })).toBeChecked();
	if (configFile) expect(readFileSync(configFile, 'utf8')).toMatch(/telemetry: true/);
});

test('TELEMETRY=off in .env: off, locked, says why', async ({ page }, info) => {
	await page.route('**/api/telemetry', async (route) => {
		if (route.request().method() !== 'GET') return route.fallback();
		const res = await route.fetch();
		const real = await res.json();
		await route.fulfill({
			response: res,
			json: { ...real, enabled: false, locked: true, next_at: undefined, next_reason: undefined }
		});
	});
	await page.goto('/settings');
	const c = await card(page);
	const sw = c.getByRole('switch', { name: /Send usage & health stats/ });
	await expect(sw).not.toBeChecked();
	await expect(sw).toBeDisabled();
	await expect(c.getByRole('note')).toContainText('turns this off, whatever the switch says');
	if (info.project.name === 'desktop') {
		await page.setViewportSize({ width: widths.phone, height: 844 });
		await page.goto('/settings');
		const cc = await card(page);
		await expect(cc.getByRole('note')).toContainText('turns this off');
		await noHorizontalOverflow(page);
		await cc.screenshot({ path: `${dir}/admin-settings-locked-phone.png` });
	}
});
