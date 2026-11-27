import { expect, test } from '@playwright/test';

// A short window: the rail scrolls, so the account block at its foot
// (theme, Sign out) stays reachable instead of being cut off.
test('sign out is reachable in a short window', async ({ page }, info) => {
	test.skip(info.project.name !== 'desktop', 'the rail is a sidebar on desktop only');
	await page.setViewportSize({ width: 1280, height: 480 });
	await page.goto('/');
	const signOut = page.getByRole('button', { name: 'Sign out' });
	await signOut.scrollIntoViewIfNeeded();
	await expect(signOut).toBeInViewport();
	const rail = page.locator('.rail');
	const scrolls = await rail.evaluate((el) => el.scrollHeight > el.clientHeight);
	expect(scrolls).toBe(true);
	if (process.env.SCREENSHOT_DIR)
		await page.screenshot({ path: `${process.env.SCREENSHOT_DIR}/shell-short-window.png` });
});
