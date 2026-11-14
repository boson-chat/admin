import { expect, test, type Page } from '@playwright/test';

// Raw MJML editing on the Emails page against the panel running on the
// fake auth (e2e/fake-auth.mjs): the browser compiles with mjml-browser in
// a worker, the preview gets the compiled HTML, saves write config/emails/.
//
//   ./scripts/e2e-emails.sh emails-mjml
const fake = process.env.FAKE_AUTH_INTERNAL_URL ?? '';
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

test.skip(!fake, 'set FAKE_AUTH_INTERNAL_URL (the panel must run on e2e/fake-auth.mjs)');

const inFrame = (page: Page) => page.locator('iframe[title="Email preview"]').contentFrame();
const editor = (page: Page) => page.locator('.cm-content');
const status = (page: Page) => page.getByTestId('mjml-status');
const problems = (page: Page) => page.getByTestId('mjml-problems');

/** Replaces the editor's text, as a paste would. */
async function setSource(page: Page, text: string) {
	await editor(page).click();
	await page.keyboard.press('ControlOrMeta+a');
	await page.keyboard.insertText(text);
}

async function defaultSource(page: Page, part: string) {
	const r = await page.request.get(`/api/emails/default-mjml?part=${part}`);
	expect(r.ok()).toBe(true);
	return ((await r.json()) as { mjml: string }).mjml;
}

async function configValue(page: Page, key: string) {
	const r = await page.request.get('/api/config');
	const v = (await r.json()) as { keys: { key: string; set: boolean; value?: unknown }[] };
	const k = v.keys.find((x) => x.key === key);
	return k?.set ? k.value : undefined;
}

async function openLayout(page: Page) {
	await page.goto('/emails');
	await expect(page.getByRole('heading', { name: 'Emails', level: 1 })).toBeVisible();
	await page.getByRole('tab', { name: 'Layout (MJML)' }).click();
	await expect(editor(page)).toContainText('mj-body');
	await expect(status(page)).toContainText('Compiled');
	await expect(inFrame(page).locator('[data-content]')).toBeVisible();
}

test.afterAll(async ({ playwright }) => {
	// Leave nothing saved for the other specs.
	const api = await playwright.request.newContext({
		baseURL: process.env.BASE_URL ?? 'http://localhost:8090',
		storageState: 'e2e/.auth/admin.json'
	});
	const s = await api.get('/api/session');
	const csrf = ((await s.json()) as { csrf_token: string }).csrf_token;
	for (const part of ['layout', 'invite'])
		await api.delete(`/api/emails/mjml/${part}`, {
			headers: { 'X-CSRF-Token': csrf, 'X-Requested-With': 'boson-admin' }
		});
	await api.dispose();
});

test('edit the layout: live preview, inline errors, save, reset', async ({ page }) => {
	const saves: string[] = [];
	page.on('request', (r) => {
		if (r.url().includes('/api/emails/mjml') && r.method() !== 'GET') saves.push(r.method());
	});
	await openLayout(page);
	const base = await defaultSource(page, 'layout');
	await expect(inFrame(page).locator('.banner')).toHaveText('Example Chat');

	// Typing (well, pasting) recompiles and re-previews, unsaved.
	const edited = base.replace('>{{network}}<', '>Edited banner for {{network}}<');
	await setSource(page, edited);
	await expect(inFrame(page).locator('.banner')).toHaveText('Edited banner for Example Chat');
	await expect(page.getByText('Unsaved changes')).toBeVisible();
	expect(saves).toEqual([]);

	// A broken tag: the error is listed with its line and marked in the
	// gutter; the preview keeps the last good version; saving is off.
	const broken = edited.replace(
		'      </mj-column>\n    </mj-section>\n    <mj-section background',
		'    </mj-section>\n    <mj-section background'
	);
	const line = broken.split('\n').findIndex((l) => l.includes('<mj-column>')) + 1;
	await setSource(page, broken);
	await expect(problems(page)).toContainText(`Line ${line}`);
	await expect(problems(page)).toContainText('<mj-column> is never closed');
	await expect(status(page)).toContainText('1 error');
	await expect(page.locator('.cm-lint-marker-error')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Save layout' })).toBeDisabled();
	await expect(inFrame(page).locator('.banner')).toHaveText('Edited banner for Example Chat');

	// MJML's own validation, and the {{content}} rule.
	await setSource(page, edited.replace('font-size="15px"', 'font-size="15px" color="nope"'));
	await expect(problems(page)).toContainText('Attribute color has invalid value: nope');
	await setSource(page, edited.replace('{{content}}', 'no content'));
	await expect(problems(page)).toContainText('The layout needs {{content}} once');
	await page
		.getByRole('button', { name: /^Line \d+$/ })
		.first()
		.click();
	await expect(page.locator('.cm-editor')).toHaveClass(/cm-focused/);

	// Fixed and saved.
	await setSource(page, edited);
	await expect(status(page)).toContainText('Compiled');
	await page.getByRole('button', { name: 'Save layout' }).click();
	await expect(page.getByText('Layout saved.')).toBeVisible();
	expect(saves).toEqual(['PUT']);
	expect(await configValue(page, 'auth.email.layout.html')).toMatch(/^emails\/layout-[0-9a-f]{16}\.html$/);
	expect(await configValue(page, 'auth.email.layout.mjml')).toMatch(/^emails\/layout-[0-9a-f]{16}\.mjml$/);
	const stored = await (await page.request.get('/api/emails/mjml/layout')).json();
	expect(stored).toMatchObject({ set: true, mjml: edited });
	await expect(page.getByText('Saved', { exact: true })).toBeVisible();

	// A reload opens the saved layout.
	await openLayout(page);
	await expect(inFrame(page).locator('.banner')).toHaveText('Edited banner for Example Chat');

	// Reset to default: Boson's source back in the editor, not saved yet.
	await page.getByRole('button', { name: 'Reset to default' }).click();
	await expect(inFrame(page).locator('.banner')).toHaveText('Example Chat');
	await expect(editor(page)).not.toContainText('Edited banner');
	await expect(page.getByText('Unsaved changes')).toBeVisible();
	await page.locator('#tpl-panel').getByRole('button', { name: 'Discard' }).click();
	await expect(inFrame(page).locator('.banner')).toHaveText('Edited banner for Example Chat');

	// And back to the built-in layout altogether.
	page.once('dialog', (d) => void d.accept());
	await page.getByRole('button', { name: 'Use Boson’s layout' }).click();
	await expect(page.getByText('Emails use Boson’s built-in layout again.')).toBeVisible();
	expect(await configValue(page, 'auth.email.layout.html')).toBeUndefined();
	await expect(editor(page)).not.toContainText('Edited banner');
	expect(saves).toEqual(['PUT', 'DELETE']);
});

test('an email with its own MJML: {{link}} required, auth’s warnings shown', async ({ page }) => {
	await page.goto('/emails');
	await page.getByRole('tab', { name: 'Invite' }).click();
	await page.getByRole('button', { name: 'MJML', exact: true }).click();
	await expect(editor(page)).toContainText('mj-button');
	await expect(status(page)).toContainText('Compiled');
	const link = inFrame(page).getByRole('link', { name: 'Open link' });
	await expect(link).toHaveAttribute('href', 'https://chat.example.com/verify?token=example');

	const base = await defaultSource(page, 'invite');
	await setSource(page, base.replace('href="{{link}}"', 'href="https://example.com"'));
	await expect(problems(page)).toContainText('This email needs {{link}}');
	await expect(page.getByRole('button', { name: 'Save email' })).toBeDisabled();

	// A placeholder auth doesn't know: a warning here, and auth's own.
	const own = base.replace('Open link', 'Join {{mystery}} now');
	await setSource(page, own);
	await expect(problems(page)).toContainText('{{mystery}} isn’t a placeholder auth fills');
	await expect(page.getByTestId('pv-warnings')).toContainText('{{mystery}}');
	await expect(inFrame(page).getByRole('link', { name: 'Join {{mystery}} now' })).toBeVisible();

	// Placeholder chips insert at the cursor.
	await editor(page).click();
	await page.keyboard.press('ControlOrMeta+End');
	await page.getByRole('button', { name: '{{username}}' }).click();
	await expect(editor(page)).toContainText('</mjml>{{username}}');
	await setSource(page, own);

	await page.getByRole('button', { name: 'Save email' }).click();
	await expect(page.getByText('Invite email saved with its own MJML.')).toBeVisible();
	expect(await configValue(page, 'auth.email.templates.invite.html')).toMatch(/^emails\/invite-/);

	// Text mode says the HTML comes from the MJML now.
	await page.getByRole('button', { name: 'Text', exact: true }).click();
	await expect(page.getByText('This email has its own MJML')).toBeVisible();
	await page.getByRole('button', { name: 'MJML', exact: true }).click();
	page.once('dialog', (d) => void d.accept());
	await page.getByRole('button', { name: 'Remove own MJML' }).click();
	await expect(page.getByText('Invite email uses the layout again.')).toBeVisible();
	expect(await configValue(page, 'auth.email.templates.invite.html')).toBeUndefined();
});

test('asks before leaving with unsaved MJML', async ({ page }) => {
	await openLayout(page);
	await editor(page).click();
	await page.keyboard.press('ControlOrMeta+End');
	await page.keyboard.insertText('\n<!-- unsaved -->');
	await expect(page.getByText('Unsaved changes')).toBeVisible();
	let asked = '';
	page.once('dialog', (d) => {
		asked = d.message();
		void d.dismiss();
	});
	await page.getByRole('link', { name: 'Branding' }).first().click();
	await expect.poll(() => asked).toMatch(/unsaved email changes/);
	await expect(page).toHaveURL(/\/emails$/);
});

for (const mode of ['light', 'dark'] as const) {
	test(`screenshot (${mode})`, async ({ page }, info) => {
		await page.addInitScript((m) => localStorage.setItem('theme', m), mode);
		await openLayout(page);
		await page.getByRole('button', { name: mode === 'dark' ? 'Dark' : 'Light' }).click();
		await expect(page.getByText('Updating…')).toBeHidden();
		const [scroll, width] = await page.evaluate(() => [
			document.documentElement.scrollWidth,
			document.documentElement.clientWidth
		]);
		expect(scroll).toBeLessThanOrEqual(width);
		await page.waitForTimeout(250);
		await page
			.locator('#tpl-panel')
			.screenshot({ path: `${dir}/emails-mjml-${info.project.name}-${mode}.png` });

		// With problems listed.
		const base = await defaultSource(page, 'layout');
		await setSource(
			page,
			base
				.replace('font-size="12px"', 'font-size="12px" padding="wide"')
				.replace('{{footer}}', '{{footer}} {{mystery}}')
		);
		await expect(problems(page)).toContainText('{{mystery}}');
		await expect(page.getByText('Updating…')).toBeHidden();
		await page.waitForTimeout(250);
		const vp = page.viewportSize()!;
		const h = await page.evaluate(() => document.documentElement.scrollHeight);
		await page.setViewportSize({ width: vp.width, height: Math.max(vp.height, h) });
		await page.waitForTimeout(250);
		await page.screenshot({ path: `${dir}/emails-mjml-page-${info.project.name}-${mode}.png` });
		await page.setViewportSize(vp);
	});
}
