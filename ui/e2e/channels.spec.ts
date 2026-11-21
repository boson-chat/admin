import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';

// Role channels (Roles → a role → Channels) and the Channel access page,
// against the panel on the fake auth with roles (FAKE_AUTH_ROLES=1):
//
//   ./scripts/e2e-channels.sh  # SCREENSHOT_DIR=... keeps screenshots
const fake = process.env.FAKE_AUTH_INTERNAL_URL ?? '';
const configFile = process.env.BOSON_CONFIG_FILE ?? '';
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

test.skip(
	!fake,
	'set FAKE_AUTH_INTERNAL_URL (the panel must run on FAKE_AUTH_ROLES=1; scripts/e2e-channels.sh)'
);

async function noHorizontalOverflow(page: Page) {
	const [scroll, width] = await page.evaluate(() => [
		document.documentElement.scrollWidth,
		document.documentElement.clientWidth
	]);
	expect(scroll).toBeLessThanOrEqual(width);
}

async function shoot(page: Page, name: string) {
	await noHorizontalOverflow(page);
	await page.waitForTimeout(200);
	const vp = page.viewportSize()!;
	const h = await page.evaluate(() => document.documentElement.scrollHeight);
	await page.setViewportSize({ width: vp.width, height: Math.max(vp.height, h) });
	await page.waitForTimeout(200);
	await page.screenshot({ path: `${dir}/${name}.png` });
	await page.setViewportSize(vp);
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

test.beforeEach(async ({ page }) => {
	expect((await page.request.post(`${fake}/__reset`)).ok()).toBe(true);
	await patchConfig(page, { unset: ['roles.channels.autojoin', 'roles.channels.rejoin'] });
});

async function audit(page: Page): Promise<{ event: string; role_id?: string }[]> {
	return (await (await page.request.get(`${fake}/__audit`)).json()).audit;
}

test('a role’s channels: add, auto-join, preview, save', async ({ page }) => {
	await page.goto('/roles/rol_events?tab=channels');
	await expect(page.getByRole('heading', { name: 'Event hosts' })).toBeVisible();
	await expect(page.getByText('No channels for Event hosts')).toBeVisible();

	const add = page.getByRole('textbox', { name: 'Add a channel' });
	await add.fill('events');
	await add.press('Enter');
	await expect(page.getByText('A channel name starts with #, like #staff.')).toBeVisible();
	await add.fill('#Events');
	await add.press('Enter');
	await add.fill('#staff');
	await add.press('Enter');
	const list = page.getByRole('list', { name: 'Channels' });
	await expect(list.getByText('#events')).toBeVisible();
	await expect(list.locator('li', { hasText: '#staff' }).getByText('Private')).toBeVisible();
	await expect(list.locator('li', { hasText: '#events' }).getByText('Public')).toBeVisible();
	await expect(page.getByTestId('channel-preview')).toContainText(
		'members of Event hosts would be joined to #events and #staff'
	);
	await list.locator('li', { hasText: '#staff' }).getByRole('checkbox', { name: 'Auto-join' }).uncheck();
	await expect(page.getByTestId('channel-preview')).toContainText('would be joined to #events.');
	await page.getByRole('button', { name: 'Save channels' }).click();
	await expect(page.getByText('Saved Event hosts’s channels.')).toBeVisible();
	await expect(page.getByText('All changes saved')).toBeVisible();
	expect((await audit(page)).at(-1)).toMatchObject({ event: 'role_channels_updated', role_id: 'rol_events' });

	// Member's tab edits everyone's channels
	await page.goto('/roles/rol_member?tab=channels');
	await expect(page.getByTestId('channel-preview')).toContainText('everyone would be joined to #lobby.');
});

test('channel access: auto-join switches and private channels', async ({ page }) => {
	await page.goto('/channels');
	await expect(page.getByRole('heading', { name: 'Channel access', level: 1 })).toBeVisible();
	await expect(
		page.getByText(/With auto-join on, members of Mods would be joined to #mods and #staff\./)
	).toBeVisible();

	await page
		.getByRole('switch', { name: /Auto-join members to the channels assigned to their roles/ })
		.check({ force: true });
	await page.getByText('Leave them out', { exact: true }).click();
	await page.getByRole('button', { name: 'Save auto-join' }).click();
	await expect(page.getByText('Auto-join saved.', { exact: false })).toBeVisible();
	await expect(page.getByText('Members of Mods will be joined to #mods and #staff.')).toBeVisible();
	if (configFile) {
		const yaml = readFileSync(configFile, 'utf8');
		expect(yaml).toMatch(/roles:\s+channels:\s+autojoin: true\s+rejoin: once/);
	}

	await page.getByRole('switch', { name: '#mods is private' }).check({ force: true });
	await page.getByRole('textbox', { name: 'Make a channel private' }).fill('#secret');
	await page.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(page.getByText('No role lists it, so only IRC operators can join.')).toBeVisible();
	await page.getByRole('button', { name: 'Save channel access' }).click();
	await expect(page.getByText(/Saved\. \d channels are private\./)).toBeVisible();
	expect((await audit(page)).at(-1)).toMatchObject({ event: 'channel_access_updated' });
});

for (const mode of ['light', 'dark'] as const) {
	test(`screenshots (${mode})`, async ({ page }, info) => {
		await patchConfig(page, { set: { 'roles.channels.autojoin': true } });
		await page.addInitScript((m) => localStorage.setItem('theme', m), mode);
		await page.goto('/channels');
		await expect(page.getByRole('list', { name: 'Channels' })).toBeVisible();
		await shoot(page, `channels-${info.project.name}-${mode}`);

		await page.goto('/roles/rol_mods?tab=channels');
		await expect(page.getByRole('list', { name: 'Channels' })).toBeVisible();
		await shoot(page, `role-channels-${info.project.name}-${mode}`);
	});
}
