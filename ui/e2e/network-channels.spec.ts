import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';

// Network channels and channel creation (Network settings) and the role
// channels auto-join switch (Channel access), against the panel on the
// fake auth with roles (FAKE_AUTH_ROLES=1):
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

/** The whole page, and the network channels group on its own. */
async function shoot(page: Page, name: string, focus?: string) {
	await noHorizontalOverflow(page);
	await page.waitForTimeout(200);
	if (focus)
		await page
			.locator(focus)
			.first()
			.screenshot({ path: `${dir}/${name}-part.png` });
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

const keys = [
	'irc.channels',
	'irc.channel_founder',
	'irc.autojoin_guests',
	'auth.channel_creation',
	'roles.channels.autojoin'
];

test.beforeEach(async ({ page }) => {
	expect((await page.request.post(`${fake}/__reset`, { data: {} })).ok()).toBe(true);
	await patchConfig(page, { set: { 'network.name': 'Example Chat' }, unset: keys });
});

const group = 'fieldset:has(legend:text("Network channels"))';

test('network channels: add, topic, auto-join, founder, save to the file', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByRole('heading', { name: 'Network settings', level: 1 })).toBeVisible();
	await expect(page.getByTestId('network-channels-empty')).toBeVisible();

	const add = page.getByRole('textbox', { name: 'Add a channel' });
	await add.fill('lobby');
	await add.press('Enter');
	await expect(page.getByText('A channel name starts with #, like #lobby.')).toBeVisible();
	await add.fill('#lobby');
	await add.press('Enter');
	await add.fill('#help');
	await add.press('Enter');
	await page.getByRole('textbox', { name: 'Topic of #lobby' }).fill('Welcome to Example Chat');
	await page.getByRole('textbox', { name: 'Founder account' }).fill('admin');
	await page.getByRole('radio', { name: /Only admins/ }).check({ force: true });
	await expect(page.getByTestId('admins-only-note')).toBeVisible();
	await page.getByRole('button', { name: 'Save changes' }).click();
	await expect(page.getByText('Network settings saved.')).toBeVisible();
	await expect(page.getByText(/unsaved change/)).toHaveCount(0);

	if (configFile) {
		const yaml = readFileSync(configFile, 'utf8');
		expect(yaml).toMatch(
			/channels:\s+- name: "#lobby"\s+topic: Welcome to Example Chat\s+autojoin: true\s+- name: "#help"\s+autojoin: false/
		);
		expect(yaml).toMatch(/channel_founder: admin/);
		expect(yaml).toMatch(/channel_creation: admins/);
	}

	// Reloaded from the file.
	await page.reload();
	await expect(page.getByRole('checkbox', { name: 'Auto-join #lobby' })).toBeChecked();
	await expect(page.getByRole('checkbox', { name: 'Auto-join #help' })).not.toBeChecked();
	await expect(page.getByRole('radio', { name: /Only admins/ })).toBeChecked();
	await expect(page.locator(group)).toContainText('founder: admin');
});

test('network channels: who can join, kept to roles in the file', async ({ page }) => {
	await page.goto('/settings');
	const add = page.getByRole('textbox', { name: 'Add a channel' });
	await add.fill('#lobby');
	await add.press('Enter');
	await add.fill('#opers');
	await add.press('Enter');

	const who = page.getByRole('combobox', { name: 'Who can join #opers' });
	await expect(who).toHaveText('Everyone');
	await who.click();
	await expect(page.getByRole('option', { name: /Member/ })).toContainText(
		'Any signed-in account, no guests'
	);
	await page.getByRole('option', { name: 'Admin', exact: true }).click();
	await page.getByRole('option', { name: /^Owner/ }).click();
	await page.keyboard.press('Escape');
	await expect(who).toHaveText('Owner or Admin');
	await expect(page.getByTestId('restricted-summary')).toHaveText('#opers restricted to Owner or Admin');
	await expect(page.getByRole('combobox', { name: 'Who can join #lobby' })).toHaveText('Everyone');
	await page.getByRole('button', { name: 'Save changes' }).click();
	await expect(page.getByText('Network settings saved.')).toBeVisible();

	if (configFile) {
		const yaml = readFileSync(configFile, 'utf8');
		expect(yaml).toMatch(/- name: "#opers"\s+autojoin: false\s+roles: \[Owner, Admin\]/);
		expect(yaml.match(/roles:/g)?.length).toBe(1);
	}

	// Reloaded from the file; a name no role has shows as such and can go.
	await patchConfig(page, {
		set: { 'irc.channels': [{ name: '#opers', autojoin: false, roles: ['admin', 'Old Staff'] }] }
	});
	await page.goto('/settings');
	const again = page.getByRole('combobox', { name: 'Who can join #opers' });
	await expect(again).toHaveText('Admin or Old Staff');
	await again.click();
	await expect(page.getByRole('option', { name: /Old Staff/ })).toContainText('No such role');
	await page.getByRole('option', { name: /Old Staff/ }).click();
	await page.getByRole('option', { name: 'Admin', exact: true }).click();
	await page.keyboard.press('Escape');
	await expect(again).toHaveText('Everyone');
	await expect(page.getByTestId('restricted-summary')).toHaveCount(0);
	await page.getByRole('button', { name: 'Save changes' }).click();
	await expect(page.getByText('Network settings saved.')).toBeVisible();
	if (configFile) expect(readFileSync(configFile, 'utf8')).not.toMatch(/roles:/);
});

test('role channels auto-join: says when no role has channels', async ({ page }) => {
	expect((await page.request.post(`${fake}/__reset`, { data: { role_channels: false } })).ok()).toBe(true);
	await page.goto('/channels');
	await expect(page.getByTestId('no-role-channels')).toContainText(
		'No role has channels yet — assign some in Roles → Channels'
	);
	await page.getByRole('link', { name: 'Roles → Channels' }).click();
	await expect(page).toHaveURL(/\/roles/);
});

for (const mode of ['light', 'dark'] as const) {
	test(`screenshots (${mode})`, async ({ page }, info) => {
		const tag = `${info.project.name}-${mode}`;
		await page.addInitScript((m) => localStorage.setItem('theme', m), mode);

		await page.goto('/settings');
		await expect(page.getByTestId('network-channels-empty')).toBeVisible();
		await shoot(page, `settings-network-channels-empty-${tag}`, group);

		await patchConfig(page, {
			set: {
				'irc.channels': [
					{ name: '#lobby', topic: 'Welcome to Example Chat — be nice', autojoin: true },
					{ name: '#help', autojoin: false }
				]
			}
		});
		await page.goto('/settings');
		await expect(page.getByRole('checkbox', { name: 'Auto-join #lobby' })).toBeChecked();
		await shoot(page, `settings-network-channels-${tag}`, group);

		await patchConfig(page, {
			set: {
				'irc.channels': [
					{ name: '#lobby', topic: 'Welcome to Example Chat — be nice', autojoin: true },
					{ name: '#opers', topic: 'Staff room', autojoin: true, roles: ['Owner', 'Admin'] },
					{ name: '#members', autojoin: false, roles: ['Member'] },
					{ name: '#help', autojoin: false }
				]
			}
		});
		await page.goto('/settings');
		await expect(page.getByTestId('restricted-summary')).toBeVisible();
		await shoot(page, `settings-network-channels-roles-${tag}`, group);
		await page.getByRole('combobox', { name: 'Who can join #opers' }).click();
		await expect(page.getByRole('option', { name: /Member/ })).toBeVisible();
		await shoot(page, `settings-network-channels-roles-open-${tag}`);
		await page.keyboard.press('Escape');

		await patchConfig(page, { set: { 'auth.channel_creation': 'admins' } });
		await page.goto('/settings');
		await expect(page.getByTestId('admins-only-note')).toBeVisible();
		await shoot(page, `settings-admins-only-${tag}`, 'section[aria-labelledby="ch-h"]');

		expect((await page.request.post(`${fake}/__reset`, { data: { role_channels: false } })).ok()).toBe(true);
		await patchConfig(page, { set: { 'roles.channels.autojoin': true } });
		await page.goto('/channels');
		await expect(page.getByTestId('no-role-channels')).toBeVisible();
		await shoot(page, `channels-no-role-channels-${tag}`, 'section[aria-labelledby="autojoin-h"]');
	});
}
