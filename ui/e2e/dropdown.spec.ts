import { expect, test, type Page } from '@playwright/test';

async function noHorizontalOverflow(page: Page) {
	const [scroll, width] = await page.evaluate(() => [
		document.documentElement.scrollWidth,
		document.documentElement.clientWidth
	]);
	expect(scroll).toBeLessThanOrEqual(width);
}

test.beforeEach(async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('heading', { name: 'Stack', level: 1 })).toBeVisible();
});

test('keyboard: open, move, type-ahead, choose, escape', async ({ page }) => {
	await page.goto('/audit');
	const when = page.getByRole('combobox', { name: 'From' });
	await when.focus();
	await page.keyboard.press('ArrowDown');
	await expect(when).toHaveAttribute('aria-expanded', 'true');
	const list = page.getByRole('listbox', { name: 'From' });
	await expect(list).toBeVisible();
	await noHorizontalOverflow(page);

	await page.keyboard.press('Home');
	await expect(page.locator('#' + (await when.getAttribute('aria-activedescendant')))).toHaveText(
		'Last hour'
	);
	await page.keyboard.press('End');
	await expect(page.locator('#' + (await when.getAttribute('aria-activedescendant')))).toHaveText('Any time');
	await page.keyboard.press('Escape');
	await expect(list).toBeHidden();
	await expect(when).toBeFocused();

	// type-ahead from closed: "l", "l" → second option starting with L
	await page.keyboard.type('ll');
	await expect(list).toBeVisible();
	await expect(page.locator('#' + (await when.getAttribute('aria-activedescendant')))).toHaveText(
		'Last 24 hours'
	);
	await page.keyboard.press('Enter');
	await expect(page).toHaveURL(/since=24h/);
	await expect(when).toHaveText(/Last 24 hours/);
	await expect(when).toBeFocused();

	// Space opens; Tab chooses the highlighted option and moves on
	await page.keyboard.press(' ');
	await expect(list).toBeVisible();
	await page.keyboard.press('ArrowDown');
	await page.keyboard.press('Tab');
	await expect(list).toBeHidden();
	await expect(page).toHaveURL(/since=168h/);
	await expect(when).not.toBeFocused();
});

test('mouse: open, choose, click outside closes', async ({ page }) => {
	await page.goto('/audit');
	const ev = page.getByRole('combobox', { name: 'Event' });
	await ev.click();
	const list = page.getByRole('listbox', { name: 'Event' });
	await expect(list).toBeVisible();
	await expect(list.getByRole('group', { name: 'Groups' })).toBeVisible();
	await noHorizontalOverflow(page);
	await list.getByRole('option', { name: 'Admin actions' }).click();
	await expect(list).toBeHidden();
	await expect(page).toHaveURL(/event=admin_\*/);
	await expect(ev).toHaveText(/Admin actions/);

	await ev.click();
	await expect(list).toBeVisible();
	await page.getByRole('heading', { name: 'Audit log' }).click();
	await expect(list).toBeHidden();
	await expect(page).toHaveURL(/event=admin_\*/);
});

test('invite uses: custom reveals a number field; values post with the form', async ({ page }) => {
	await page.goto('/invites');
	const uses = page.getByRole('combobox', { name: 'Uses' });
	await uses.click();
	await page.getByRole('option', { name: 'Custom…' }).click();
	const n = page.getByRole('spinbutton', { name: 'Number of uses' });
	await expect(n).toBeFocused();
	await n.fill('3');
	const ttl = page.getByRole('combobox', { name: 'Expires after' });
	await ttl.click();
	await page.getByRole('option', { name: '1 day' }).click();
	const [req] = await Promise.all([
		page.waitForRequest((r) => r.url().endsWith('/api/invites') && r.method() === 'POST'),
		page.getByRole('button', { name: 'Create invite' }).click()
	]);
	expect(req.postDataJSON()).toMatchObject({ max_uses: 3, ttl: '24h' });
});

test('theme dropdown flips up at the bottom of the rail and switches theme', async ({ page, isMobile }) => {
	if (isMobile) await page.getByRole('button', { name: 'Menu' }).click();
	const theme = page.getByRole('combobox', { name: 'Theme' });
	await theme.click();
	const list = page.getByRole('listbox', { name: 'Theme' });
	await expect(list).toBeVisible();
	if (!isMobile) {
		const [t, l] = await Promise.all([theme.boundingBox(), list.boundingBox()]);
		expect(l!.y + l!.height).toBeLessThanOrEqual(t!.y); // above the trigger
	}
	await noHorizontalOverflow(page);
	await list.getByRole('option', { name: 'Light' }).click();
	await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
	await theme.click();
	await page.keyboard.press('Home');
	await page.keyboard.press('Enter');
	await expect(page.locator('html')).not.toHaveAttribute('data-theme', /.+/);
});

test('free text: NameID format takes any value and suggests the common ones', async ({ page }) => {
	await page.goto('/sso/new');
	await page.locator('input[type=radio][value=okta]').check({ force: true });
	const nameid = page.getByRole('combobox', { name: 'NameID format' });
	await expect(nameid).toHaveAttribute('aria-autocomplete', 'list');
	await nameid.click();
	await page.keyboard.type('spec');
	const list = page.getByRole('listbox', { name: 'NameID format' });
	await expect(list.getByRole('option')).toHaveText([/unspecified/]);
	await noHorizontalOverflow(page);
	await page.keyboard.press('ArrowDown');
	await page.keyboard.press('Enter');
	await expect(nameid).toHaveValue('unspecified');
	await expect(list).toBeHidden();

	await nameid.fill('urn:oasis:names:tc:SAML:2.0:nameid-format:transient');
	await expect(nameid).toHaveAttribute('aria-expanded', 'false');
	// the chevron offers every suggestion even when the text matches none
	await page.getByRole('button', { name: 'Show suggestions' }).click();
	await expect(list.getByRole('option')).toHaveCount(3);
	await list.getByRole('option', { name: /email/ }).click();
	await expect(nameid).toHaveValue('email');
	await expect(list).toBeHidden();
});

test('stack overview names the edge and probes IRC', async ({ page }) => {
	await expect(page.getByText(/, TLS for every hostname/)).toBeVisible();
	await expect(
		page.getByText('Caddy, TLS for every hostname').or(page.getByText('Traefik, TLS for every hostname'))
	).toBeVisible();
	await expect(page.getByRole('heading', { name: 'IRC', level: 3 })).toBeVisible();
	await expect(page.getByText('Not monitored here yet')).toHaveCount(0);
});

test('stack overview times public endpoints from the browser', async ({ page }) => {
	const violations: string[] = [];
	page.on('console', (m) => {
		if (/Content Security Policy/i.test(m.text())) violations.push(m.text());
	});
	await page.reload();
	const you = page.locator('.lat .you');
	await expect(you.first()).toBeVisible();
	// every probe settles to a number or "n/a"
	await expect(page.locator('.lat .you .v', { hasText: '…' })).toHaveCount(0, { timeout: 15_000 });
	await expect(page.locator('.lat .int').first()).toContainText('internal');
	await expect(page.locator('.edge-lat')).toContainText(/ms|n\/a/);
	expect(violations).toEqual([]);
});
