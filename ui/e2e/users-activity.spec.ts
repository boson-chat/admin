import { expect, test, type Page } from '@playwright/test';

// Users list "Last active" column (sortable) and the detail page's "Last
// signed in" / "Last active", against the fake auth:
//
//   ./scripts/e2e-users.sh       # SCREENSHOT_DIR=... keeps screenshots
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';
const fake = process.env.FAKE_AUTH_INTERNAL_URL ?? '';
test.skip(
	!fake,
	'set FAKE_AUTH_INTERNAL_URL (the panel must run on e2e/fake-auth.mjs; scripts/e2e-users.sh)'
);

async function mode(m: 'old' | 'new') {
	const r = await fetch(`${fake}/__mode`, { method: 'POST', body: JSON.stringify({ mode: m }) });
	expect(r.ok).toBeTruthy();
}

const rows = (page: Page) => page.locator('tbody tr');
const names = (page: Page) => rows(page).locator('a.name').allTextContents();

test.afterEach(() => mode('new'));

test('users list shows last active, sortable', async ({ page }, info) => {
	await mode('new');
	await page.goto('/users');
	const header = page.getByRole('columnheader', { name: /last active/i });
	await expect(header).toBeVisible();
	await expect(header).toHaveAttribute('aria-sort', 'none');
	// Default order: newest account first.
	await expect.poll(() => names(page)).toEqual(['newcomer', 'maya', 'ravi', 'admin']);

	const row = (name: string) => rows(page).filter({ has: page.locator('a.name', { hasText: name }) });
	const seen = (name: string) => row(name).locator('td.seen');
	await expect(seen('admin')).toHaveText('3m ago');
	await expect(seen('maya')).toHaveText('3h ago');
	await expect(seen('ravi')).toHaveText(/^[A-Z][a-z]{2} \d{1,2}, \d{4}$/); // over a month: the date
	await expect(seen('newcomer')).toHaveText('Never');
	// Status stays visible (its own column, or under the name on phones).
	await expect(row('ravi').getByText('Disabled').filter({ visible: true })).toHaveCount(1);
	// Full timestamp on hover.
	await expect(seen('maya').locator('time')).toHaveAttribute('title', /\d{4}, \d{1,2}:\d{2}/);

	for (const scheme of ['light', 'dark'] as const) {
		await page.emulateMedia({ colorScheme: scheme });
		await page.screenshot({ path: `${dir}/users-list-${info.project.name}-${scheme}.png`, fullPage: true });
	}

	await header.getByRole('button').click();
	await expect(page).toHaveURL(/sort=last_active/);
	await expect(header).toHaveAttribute('aria-sort', 'descending');
	await expect.poll(() => names(page)).toEqual(['admin', 'maya', 'ravi', 'newcomer']);
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.screenshot({ path: `${dir}/users-list-sorted-${info.project.name}-dark.png`, fullPage: true });

	// Back to newest accounts (Joined is hidden on small screens).
	const joined = page.getByRole('columnheader', { name: /joined/i });
	if (await joined.isVisible()) {
		await joined.getByRole('button').click();
		await expect(page).not.toHaveURL(/sort=/);
		await expect.poll(() => names(page)).toEqual(['newcomer', 'maya', 'ravi', 'admin']);
	}
	// The sort survives a reload.
	await page.goto('/users?sort=last_active');
	await expect.poll(() => names(page)).toEqual(['admin', 'maya', 'ravi', 'newcomer']);
});

test('user detail shows last signed in and last active', async ({ page }, info) => {
	await mode('new');
	await page.goto('/users/usr_maya');
	const fact = (label: string) =>
		page.locator('.facts > div').filter({ has: page.getByText(label, { exact: true }) });
	await expect(fact('Last signed in')).toContainText('2d ago');
	await expect(fact('Last signed in')).toContainText('corp (OIDC)');
	await expect(fact('Last signed in')).toContainText('from 198.51.100.7');
	await expect(fact('Last active')).toContainText('3h ago');
	for (const scheme of ['light', 'dark'] as const) {
		await page.emulateMedia({ colorScheme: scheme });
		await page
			.locator('.facts')
			.screenshot({ path: `${dir}/user-detail-${info.project.name}-${scheme}.png` });
	}
	await page.goto('/users/usr_new');
	await expect(fact('Last signed in')).toContainText('Never');
	await expect(fact('Last active')).toContainText('Never');
	await page.goto('/users/usr_ravi');
	await expect(fact('Last signed in')).toContainText('okta (SAML)');
	await expect(fact('Last signed in')).not.toContainText('from');
});

test('an auth without activity hides it', async ({ page }) => {
	await mode('old');
	await page.goto('/users');
	await expect.poll(() => names(page)).toHaveLength(4);
	await expect(page.getByRole('columnheader', { name: /last active/i })).toHaveCount(0);
	await page.goto('/users/usr_maya');
	await expect(page.locator('.facts')).toBeVisible();
	await expect(page.getByText('Last signed in')).toHaveCount(0);
});
