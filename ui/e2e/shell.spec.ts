import { expect, test } from '@playwright/test';

// A short window: the account block (theme, Sign out) stays pinned at the
// foot of the rail and the menu links above it scroll.
test('sign out stays visible and the links scroll in a short window', async ({ page }, info) => {
	test.skip(info.project.name !== 'desktop', 'the rail is a sidebar on desktop only');
	await page.setViewportSize({ width: 1280, height: 480 });
	await page.goto('/');
	await expect(page.getByRole('button', { name: 'Sign out' })).toBeInViewport();
	const links = page.locator('nav .links');
	expect(await links.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
	await links.evaluate((el) => (el.scrollTop = el.scrollHeight));
	await expect(page.getByRole('link', { name: 'Federation' })).toBeInViewport();
	await expect(page.getByRole('button', { name: 'Sign out' })).toBeInViewport();
	if (process.env.SCREENSHOT_DIR)
		await page.screenshot({ path: `${process.env.SCREENSHOT_DIR}/shell-short-window.png` });
});

test('the open phone menu fits the screen', async ({ page }, info) => {
	test.skip(info.project.name !== 'mobile', 'phone layout only');
	await page.goto('/');
	await page.getByRole('button', { name: 'Menu' }).click();
	const nav = page.locator('nav#nav');
	const box = await nav.boundingBox();
	const vh = page.viewportSize()!.height;
	expect(box!.y + box!.height).toBeLessThanOrEqual(vh + 1);
	await expect(page.getByRole('button', { name: 'Sign out' })).toBeInViewport();
	if (process.env.SCREENSHOT_DIR)
		await page.screenshot({ path: `${process.env.SCREENSHOT_DIR}/shell-phone-menu.png` });
});
