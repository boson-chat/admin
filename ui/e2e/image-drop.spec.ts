import { expect, test, type JSHandle, type Page } from '@playwright/test';

// Dragging files onto a Branding image slot, against a running panel with
// a stack config file. The drag-over state is screenshotted into
// SCREENSHOT_DIR for a look.
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';
const logoSlot = 'role=group[name="Logo"s]';

const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="#0ea5e9"/><circle cx="32" cy="32" r="14" fill="#fff"/></svg>`;

type FileSpec = { name: string; type: string; body: string };

/** A DataTransfer holding the files, built in the page (Playwright can't serialise one). */
function transfer(page: Page, files: FileSpec[]): Promise<JSHandle<DataTransfer>> {
	return page.evaluateHandle((files) => {
		const dt = new DataTransfer();
		for (const f of files) dt.items.add(new File([f.body], f.name, { type: f.type }));
		return dt;
	}, files);
}

async function drop(page: Page, selector: string, files: FileSpec[]) {
	const dataTransfer = await transfer(page, files);
	for (const type of ['dragenter', 'dragover', 'drop'])
		await page.dispatchEvent(selector, type, { dataTransfer });
}

async function openBranding(page: Page) {
	await page.goto('/branding');
	await expect(page.getByRole('heading', { name: 'Branding', level: 1 })).toBeVisible();
	await expect(page.locator(logoSlot)).toBeVisible();
}

test.describe.configure({ mode: 'serial' });

test('dropping an image onto the logo slot uploads it', async ({ page }) => {
	await openBranding(page);
	const slot = page.locator(logoSlot);
	await expect(slot.getByText('or drop an image here')).toBeVisible();
	const put = page.waitForResponse(
		(r) => r.request().method() === 'PUT' && r.url().endsWith('/api/branding/images/logo')
	);
	await drop(page, logoSlot, [
		{ name: 'logo.svg', type: 'image/svg+xml', body: svg },
		{ name: 'ignored.txt', type: 'text/plain', body: 'second file is ignored' }
	]);
	const res = await put;
	expect(res.status()).toBe(200);
	expect(res.request().headers()['content-type']).toBe('image/svg+xml');
	await expect(page.getByText('Logo saved.')).toBeVisible();
	await expect(slot.locator('.thumb img')).toHaveAttribute('src', /\/api\/branding\/assets\//);
	await expect(slot).not.toHaveClass(/\bover\b/);
});

test('dropping a text file is rejected without uploading', async ({ page }) => {
	await openBranding(page);
	let uploads = 0;
	page.on('request', (r) => {
		if (r.method() === 'PUT' && r.url().includes('/api/branding/images/')) uploads++;
	});
	await drop(page, logoSlot, [{ name: 'notes.txt', type: 'text/plain', body: 'hello' }]);
	await expect(page.locator(logoSlot).getByRole('alert')).toHaveText(
		'That’s not a PNG, JPEG, WebP, GIF or SVG image.'
	);
	await page.waitForTimeout(300);
	expect(uploads).toBe(0);
});

test('a file dropped elsewhere on the page does not navigate', async ({ page }) => {
	await openBranding(page);
	const dataTransfer = await transfer(page, [{ name: 'logo.svg', type: 'image/svg+xml', body: svg }]);
	await page.dispatchEvent('h1', 'dragover', { dataTransfer });
	const prevented = await page.evaluate((dt) => {
		const e = new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: dt });
		document.querySelector('h1')!.dispatchEvent(e);
		return e.defaultPrevented;
	}, dataTransfer);
	expect(prevented).toBe(true);
	await expect(page).toHaveURL(/\/branding$/);
});

for (const mode of ['dark', 'light'] as const) {
	test(`drag-over state (${mode})`, async ({ page }, info) => {
		await page.addInitScript((m) => localStorage.setItem('theme', m), mode);
		await openBranding(page);
		const slot = page.locator(logoSlot);
		const dataTransfer = await transfer(page, [{ name: 'logo.svg', type: 'image/svg+xml', body: svg }]);
		await page.dispatchEvent(logoSlot, 'dragenter', { dataTransfer });
		// Crossing onto a child bumps the counter; leaving the slot for it must not drop the highlight.
		await page.dispatchEvent(`${logoSlot} >> .thumb`, 'dragenter', { dataTransfer });
		await page.dispatchEvent(logoSlot, 'dragleave', { dataTransfer });
		await expect(slot).toHaveClass(/\bover\b/);
		await expect(slot.getByText('Drop to upload')).toBeVisible();
		// The slot with some room around it (the outline sits just outside its box).
		await slot.scrollIntoViewIfNeeded();
		const box = (await slot.boundingBox())!;
		const vp = page.viewportSize()!;
		const x = Math.max(0, box.x - 24);
		const y = Math.max(0, box.y - 24);
		await page.screenshot({
			path: `${dir}/image-drop-over-${info.project.name}-${mode}.png`,
			clip: {
				x,
				y,
				width: Math.min(vp.width - x, box.width + 48),
				height: Math.min(vp.height - y, box.height + 48)
			}
		});
		await page.dispatchEvent(`${logoSlot} >> .thumb`, 'dragleave', { dataTransfer });
		await expect(slot).not.toHaveClass(/\bover\b/);
	});
}
