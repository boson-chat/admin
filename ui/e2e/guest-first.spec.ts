import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';

// Guests first (Network settings): the web client opens straight into chat
// as a guest, landing on a channel. Saved to auth: in the stack config file.
//
//   ./scripts/e2e-channels.sh  # SCREENSHOT_DIR=... keeps screenshots
const fake = process.env.FAKE_AUTH_INTERNAL_URL ?? '';
const configFile = process.env.BOSON_CONFIG_FILE ?? '';
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

test.skip(!fake, 'set FAKE_AUTH_INTERNAL_URL (scripts/e2e-channels.sh)');

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
	expect((await page.request.post(`${fake}/__reset`, { data: {} })).ok()).toBe(true);
	await patchConfig(page, {
		set: {
			'network.name': 'Example Chat',
			'irc.channels': [
				{ name: '#IRCForever', autojoin: true },
				{ name: '#help', autojoin: false },
				{ name: '#staff', autojoin: true, roles: ['Admin'] }
			]
		},
		unset: ['auth.guest_access', 'auth.guest_first', 'auth.guest_first_channel']
	});
});

test('guests first: on, landing channel, saved to the file, off with guest access', async ({
	page
}, info) => {
	await page.goto('/settings');
	const sw = page.getByRole('switch', { name: /Guests first/ });
	await expect(sw).not.toBeChecked();
	await expect(page.getByRole('combobox', { name: 'Land on' })).toHaveCount(0);
	await sw.check({ force: true });

	const land = page.getByRole('combobox', { name: 'Land on' });
	await expect(land).toBeVisible();
	await expect(land).toHaveAttribute('placeholder', '#IRCForever (the first auto-join channel)');
	// Channels kept to roles aren't offered.
	await page.getByTestId('guest-first').getByRole('button').last().click();
	await expect(page.getByRole('option', { name: /#help/ })).toBeVisible();
	await expect(page.getByRole('option', { name: /#staff/ })).toHaveCount(0);
	await page.getByRole('option', { name: /#help/ }).click();
	await expect(land).toHaveValue('#help');
	await page.getByTestId('guest-first').scrollIntoViewIfNeeded();
	await page.getByTestId('guest-first').screenshot({ path: `${dir}/guest-first-${info.project.name}.png` });

	await page.getByRole('button', { name: 'Save changes' }).click();
	await expect(page.getByText('Network settings saved.')).toBeVisible();
	if (configFile) {
		const yaml = readFileSync(configFile, 'utf8');
		expect(yaml).toMatch(/guest_first: true/);
		expect(yaml).toMatch(/guest_first_channel: ['"]#help['"]/);
	}
	await page.reload();
	await expect(page.getByRole('switch', { name: /Guests first/ })).toBeChecked();
	await expect(page.getByRole('combobox', { name: 'Land on' })).toHaveValue('#help');

	// Turning guest access off turns guests first off with it.
	// (centred: the sticky save bar must not cover it)
	const access = page.getByRole('switch', { name: /Guest access/ });
	await access.evaluate((el) => el.scrollIntoView({ block: 'center' }));
	await access.uncheck({ force: true });
	await expect(page.getByRole('switch', { name: /Guests first/ })).toBeDisabled();
	await expect(page.getByText(/turns guests first off too/)).toBeVisible();
	await page.getByRole('button', { name: 'Save changes' }).click();
	await expect(page.getByText('Network settings saved.')).toBeVisible();
	if (configFile) {
		const yaml = readFileSync(configFile, 'utf8');
		expect(yaml).toMatch(/guest_access: false/);
		expect(yaml).toMatch(/guest_first: false/);
	}
	const [scroll, width] = await page.evaluate(() => [
		document.documentElement.scrollWidth,
		document.documentElement.clientWidth
	]);
	expect(scroll).toBeLessThanOrEqual(width);
});
