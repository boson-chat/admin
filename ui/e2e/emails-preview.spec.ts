import { expect, test, type Page } from '@playwright/test';

// The Emails page's live preview against the panel running on the fake
// auth (e2e/fake-auth.mjs), whose preview echoes the unsaved settings:
//
//   node e2e/fake-auth.mjs &      # then the panel with AUTH_URL /
//   AUTH_INTERNAL_URL pointing at it (scripts/e2e-emails.sh does both)
//   FAKE_AUTH_INTERNAL_URL=http://127.0.0.1:18681 ADMIN_PASSWORD=e2e-password npx playwright test emails-preview
const fake = process.env.FAKE_AUTH_INTERNAL_URL ?? '';
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

test.skip(!fake, 'set FAKE_AUTH_INTERNAL_URL (the panel must run on e2e/fake-auth.mjs)');

async function authMode(page: Page, mode: 'new' | 'old') {
	const r = await page.request.post(`${fake}/__mode`, { data: { mode } });
	expect(r.ok()).toBe(true);
}

const frame = (page: Page) => page.locator('iframe[title="Email preview"]');
const inFrame = (page: Page) => frame(page).contentFrame();

/** Counts config saves, so the spec can prove the preview needed none. */
function countSaves(page: Page) {
	const saves: string[] = [];
	page.on('request', (r) => {
		if (r.url().includes('/api/config') && r.method() !== 'GET') saves.push(r.method());
	});
	return saves;
}

async function setAccent(page: Page, hex: string) {
	const custom = page.getByRole('button', { name: 'Custom colour' });
	if ((await custom.getAttribute('aria-expanded')) !== 'true') await custom.click();
	await page.getByRole('textbox', { name: 'Hex colour' }).fill(hex);
}

async function open(page: Page) {
	await page.goto('/emails');
	await expect(page.getByRole('heading', { name: 'Emails', level: 1 })).toBeVisible();
	await expect(inFrame(page).locator('[data-footer]')).toBeVisible();
}

test.beforeEach(async ({ page }) => authMode(page, 'new'));
test.afterAll(async ({ browser }) => {
	const page = await browser.newPage();
	await authMode(page, 'new');
	await page.close();
});

test('typing in the settings updates the preview without saving', async ({ page }) => {
	const saves = countSaves(page);
	await open(page);
	await expect(page.getByTestId('pv-from')).toHaveText('Example Chat <no-reply@example.com>');
	await expect(page.getByTestId('legacy-note')).toBeHidden();

	await page.getByRole('textbox', { name: 'Footer' }).fill('Live footer · example.com');
	await expect(inFrame(page).locator('[data-footer]')).toHaveText('Live footer · example.com');

	await setAccent(page, '#c2410c');
	await expect(inFrame(page).locator('[data-accent]')).toHaveAttribute('data-accent', '#c2410c');

	await page.getByRole('textbox', { name: 'Sender name' }).fill('Night Owls');
	await expect(page.getByTestId('pv-from')).toHaveText('Night Owls <no-reply@example.com>');

	await page.getByText('Put the logo at the top').click();
	await expect(inFrame(page).locator('.logo')).toHaveCount(0);

	// Wording too, and the plain-text part.
	await page.getByRole('textbox', { name: 'Subject' }).fill('Hello {{username}}');
	await expect(page.getByTestId('pv-subject')).toHaveText('Hello alice');
	await page.getByRole('tab', { name: 'Text' }).click();
	await expect(page.getByTestId('pv-text')).toContainText('Live footer · example.com');
	await page.getByRole('tab', { name: 'HTML' }).click();
	expect(saves).toEqual([]);
});

test('debounces typing and keeps the last preview on a field error', async ({ page }) => {
	await open(page);
	const previews: string[] = [];
	page.on('request', (r) => {
		if (r.url().endsWith('/api/emails/preview')) previews.push(r.postDataJSON().email.footer);
	});
	const footer = page.getByRole('textbox', { name: 'Footer' });
	await footer.pressSequentially('Typed fast', { delay: 30 });
	await expect(inFrame(page).locator('[data-footer]')).toHaveText('Typed fast');
	// One render for the whole word, not one per key.
	expect(previews.length).toBeLessThanOrEqual(2);
	expect(previews.at(-1)).toBe('Typed fast');

	// auth refuses the footer: the error sits under it, the preview stays.
	await footer.fill('Typed <b>fast</b>');
	await expect(page.getByRole('alert').filter({ hasText: 'leave out < and >' })).toBeVisible();
	await expect(footer).toHaveAttribute('aria-invalid', 'true');
	await expect(inFrame(page).locator('[data-footer]')).toHaveText('Typed fast');
	await footer.fill('Fixed');
	await expect(inFrame(page).locator('[data-footer]')).toHaveText('Fixed');
	await expect(footer).toHaveAttribute('aria-invalid', 'false');
});

test('switching emails previews at once; width and colours change the frame', async ({ page }) => {
	await open(page);
	await page.getByRole('tab', { name: 'Invite' }).click();
	await expect(page.getByTestId('pv-subject')).toHaveText('bob invited you to Example Chat', {
		timeout: 250
	});

	await page.getByRole('button', { name: 'Mobile' }).click();
	await expect(frame(page)).toHaveCSS('width', '375px');
	await page.getByRole('button', { name: 'Desktop' }).click();
	await expect(frame(page)).toHaveCSS('width', '600px');

	// The frame is as tall as the email (no inner scrollbar).
	await expect
		.poll(() =>
			frame(page).evaluate((f: HTMLIFrameElement) =>
				Math.abs(f.offsetHeight - f.contentDocument!.documentElement.getBoundingClientRect().height)
			)
		)
		.toBeLessThanOrEqual(1);

	const bg = () =>
		inFrame(page)
			.locator('body')
			.evaluate((b) => getComputedStyle(b).backgroundColor);
	expect(await bg()).toBe('rgb(241, 242, 244)');
	await page.getByRole('button', { name: 'Dark' }).click();
	await expect.poll(bg).toBe('rgb(18, 18, 20)');
	await page.getByRole('button', { name: 'Light' }).click();
	await expect.poll(bg).toBe('rgb(241, 242, 244)');
});

test('warns before leaving with unsaved edits', async ({ page }) => {
	await open(page);
	await page.getByRole('textbox', { name: 'Footer' }).fill('Not saved');
	let asked = '';
	page.once('dialog', (d) => {
		asked = d.message();
		void d.dismiss();
	});
	await page.getByRole('link', { name: 'Branding' }).first().click();
	await expect.poll(() => asked).toMatch(/unsaved email changes/);
	await expect(page).toHaveURL(/\/emails$/);
});

test('an older auth: the preview works and a note explains branding', async ({ page }) => {
	await authMode(page, 'old');
	await open(page);
	await expect(page.getByTestId('legacy-note')).toHaveText(
		'Branding changes preview after saving (update auth).'
	);
	await expect(page.getByTestId('pv-from')).toHaveCount(0);
	await page.getByRole('textbox', { name: 'Footer' }).fill('Ignored by old auth');
	await page.getByRole('textbox', { name: 'Subject' }).fill('Still live');
	await expect(page.getByTestId('pv-subject')).toHaveText('Still live');
	await expect(inFrame(page).locator('[data-footer]')).toHaveText('Example Chat');
});

for (const mode of ['light', 'dark'] as const) {
	test(`screenshot (${mode})`, async ({ page }, info) => {
		await page.addInitScript((m) => localStorage.setItem('theme', m), mode);
		await open(page);
		await page.getByRole('textbox', { name: 'Footer' }).fill('Example Chat · example.com');
		await setAccent(page, '#7c3aed');
		await page.getByRole('button', { name: mode === 'dark' ? 'Dark' : 'Light' }).click();
		await expect(inFrame(page).locator('[data-accent]')).toHaveAttribute('data-accent', '#7c3aed');
		await expect(page.getByText('Updating…')).toBeHidden();
		const [scroll, width] = await page.evaluate(() => [
			document.documentElement.scrollWidth,
			document.documentElement.clientWidth
		]);
		expect(scroll).toBeLessThanOrEqual(width);
		await page.waitForTimeout(200);
		// The viewport stretched to the page, so the sticky header stays on top.
		const vp = page.viewportSize()!;
		const h = await page.evaluate(() => document.documentElement.scrollHeight);
		await page.setViewportSize({ width: vp.width, height: Math.max(vp.height, h) });
		await page.waitForTimeout(200);
		await page.screenshot({ path: `${dir}/emails-live-${info.project.name}-${mode}.png` });
		await page.setViewportSize(vp);
		await page
			.locator('section.preview')
			.screenshot({ path: `${dir}/emails-live-preview-${info.project.name}-${mode}.png` });
	});
}
