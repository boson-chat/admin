import { expect, test, type Page } from '@playwright/test';

// The Updates page against the panel running on the fake Boson host
// (dev/fakestack): ./scripts/e2e-updates.sh (SCREENSHOT_DIR=... keeps screenshots).
const fake = process.env.FAKE_STACK_URL ?? '';
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

test.skip(!fake, 'set FAKE_STACK_URL (the panel must run on dev/fakestack; scripts/e2e-updates.sh)');

async function fresh(page: Page) {
	expect((await page.request.post(`${fake}/__reset`)).ok()).toBe(true);
	await page.goto('/updates');
	await page.getByRole('button', { name: 'Check now' }).click();
	await expect(page.getByRole('button', { name: 'Check now' })).toBeEnabled();
}

async function shot(page: Page, name: string, project: string) {
	await page.screenshot({ path: `${dir}/updates-${name}-${project}.png`, fullPage: true });
}

// Right after the panel starts, its first check is still running: no
// "Nothing to update", no empty table, and the results show up by
// themselves when it finishes. GET /api/updates is held in the browser in
// the "checking, never checked" state the panel answers with then.
test('the first check still running → checking state → results without a reload', async ({ page }, info) => {
	await fresh(page);
	let held = true;
	let gets = 0;
	await page.route('**/api/updates', async (route) => {
		if (route.request().method() !== 'GET') return route.fallback();
		const res = await route.fetch();
		const real = await res.json();
		gets++;
		const body = held
			? {
					...real,
					checking: true,
					checked_at: undefined,
					next_check_at: undefined,
					available: 0,
					components: []
				}
			: real;
		await route.fulfill({ response: res, json: body });
	});
	await page.reload();
	await expect(page.getByText('Checking for updates…').first()).toBeVisible();
	await expect(page.getByRole('button', { name: 'Checking…' })).toBeDisabled();
	await expect(page.getByRole('status').getByText('Checking for updates…')).toBeVisible();
	await expect(page.getByText('Nothing to update')).toHaveCount(0);
	await expect(page.getByRole('heading', { name: 'Up to date' })).toHaveCount(0);
	await shot(page, 'checking', info.project.name);
	// It keeps asking while the check runs…
	await expect.poll(() => gets, { timeout: 10_000 }).toBeGreaterThan(2);
	// …and shows the answer once it's in.
	held = false;
	await expect(page.getByText('3 parts of this install have a newer release.')).toBeVisible({
		timeout: 10_000
	});
	await expect(page.getByRole('article', { name: /Auth/ })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Up to date' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Check now' })).toBeEnabled();
	await page.unroute('**/api/updates');
});

// A check that finished with an error says so instead of "Nothing to update".
test('a failed check → error state → try again', async ({ page }, info) => {
	await fresh(page);
	let failing = true;
	await page.route('**/api/updates{,/check}', async (route) => {
		const res = await route.fetch();
		const real = await res.json();
		const body = failing
			? { ...real, available: 0, components: [], error: 'the Docker socket proxy isn’t answering' }
			: real;
		await route.fulfill({ response: res, json: body });
	});
	await page.reload();
	const err = page.getByRole('alert').filter({ hasText: 'Couldn’t check for updates' });
	await expect(err).toBeVisible();
	await expect(err).toContainText('the Docker socket proxy isn’t answering');
	await expect(page.getByText('The last check didn’t finish.')).toBeVisible();
	await expect(page.getByText('Nothing to update')).toHaveCount(0);
	await expect(page.getByRole('heading', { name: 'Up to date' })).toHaveCount(0);
	await shot(page, 'check-failed', info.project.name);
	failing = false;
	await err.getByRole('button', { name: 'Try again' }).click();
	await expect(page.getByText('3 parts of this install have a newer release.')).toBeVisible();
	await page.unroute('**/api/updates{,/check}');
});

test('update available → changelog across versions → apply → checks pass', async ({ page }, info) => {
	await fresh(page);
	await expect(page.getByRole('heading', { name: 'Updates', level: 1 })).toBeVisible();
	await expect(page.getByText('3 parts of this install have a newer release.')).toBeVisible();
	// The nav badge (behind the menu on a phone).
	const menu = page.getByRole('button', { name: 'Menu' });
	if (await menu.isVisible()) await menu.click();
	await expect(page.getByRole('link', { name: /Updates\s*3/ })).toBeVisible();
	if (await page.getByRole('button', { name: 'Close' }).isVisible())
		await page.getByRole('button', { name: 'Close' }).click();

	const auth = page.getByRole('article', { name: /Auth/ });
	await expect(auth.getByText('4 releases behind')).toBeVisible();
	await expect(auth.getByText(/Sign-ins and token refresh pause/)).toBeVisible();
	await expect(auth.locator('summary')).toContainText('Changelog: 4 releases');
	await auth.locator('summary').click();
	const versions = await auth.locator('.release .rhead strong').allTextContents();
	expect(versions).toEqual(['1.22.0', '1.21.1', '1.21.0', '1.20.1']);
	// Markdown rendered, raw HTML in a release body shown as text.
	await expect(auth.locator('.md code', { hasText: 'WEB_CLIENT_URL' })).toBeVisible();
	await expect(auth.locator('.md script')).toHaveCount(0);
	await expect(auth.getByText("<script>alert('not rendered')</script>", { exact: false })).toBeVisible();

	const api = page.getByRole('article', { name: /API/ });
	await expect(api.getByText('Breaking', { exact: true })).toBeVisible();
	await expect(api.getByText(/NICK_CLAIM_DOMAIN/).first()).toBeVisible();
	await shot(page, 'available', info.project.name);

	await page.getByRole('checkbox', { name: 'Select Auth' }).check();
	await page.getByRole('checkbox', { name: 'Select Web client' }).check();
	await page.getByRole('button', { name: 'Update selected (2)' }).click();
	const dlg = page.getByRole('dialog');
	await expect(dlg.getByText(/Nobody is disconnected/)).toBeVisible();
	await expect(dlg.getByText('1.20.0 → 1.22.0')).toBeVisible();
	await shot(page, 'confirm', info.project.name);
	await dlg.getByRole('button', { name: 'Back up and update' }).click();

	const run = page.getByRole('region', { name: /Update/ }).first();
	await expect(page.locator('.run .tag', { hasText: 'Done' })).toBeVisible({ timeout: 20_000 });
	await expect(run.getByText('Web client loads')).toBeVisible();
	await expect(run.getByText('Auth issues a token')).toBeVisible();
	await expect(run.locator('.backup')).toContainText('/root/backups/boson/');
	await expect(page.locator('.checks li.bad')).toHaveCount(0);
	await shot(page, 'done', info.project.name);
	// Now only the api is left.
	await expect(page.getByText('1 part of this install has a newer release.')).toBeVisible();
});

test('a breaking update that fails after migrating → rollback restores the data → healthy', async ({
	page
}, info) => {
	await fresh(page);
	await page.getByRole('checkbox', { name: 'Select API' }).check();
	await page.getByRole('button', { name: 'Update selected (1)' }).click();
	const dlg = page.getByRole('dialog');
	await expect(dlg.getByText('Breaking changes.')).toBeVisible();
	const go = dlg.getByRole('button', { name: 'Back up and update' });
	await expect(go).toBeDisabled();
	await dlg.getByLabel(/I’ve read these/).check();
	await shot(page, 'breaking', info.project.name);
	await go.click();

	await expect(page.locator('.run .tag', { hasText: 'Failed' })).toBeVisible({ timeout: 20_000 });
	await expect(
		page.locator('.step.failed', { hasText: 'Wait for the services to be healthy' })
	).toBeVisible();
	await expect(page.getByText(/ran database migrations in/)).toBeVisible();
	await shot(page, 'failed', info.project.name);

	await page.getByRole('button', { name: 'Roll back' }).click();
	const rb = page.getByRole('dialog', { name: 'Roll back' });
	await expect(rb.getByLabel(/Restore the data too/)).toBeChecked();
	await rb.getByRole('button', { name: 'Roll back' }).click();
	await expect(page.locator('.run .tag', { hasText: 'Rolled back' })).toBeVisible({ timeout: 20_000 });
	await expect(page.locator('.step', { hasText: /restored databases api/ })).toBeVisible();
	await expect(page.locator('.checks li.bad')).toHaveCount(0);
	await expect(page.locator('.checks li', { hasText: 'API version' })).toContainText('0.57.0');
	const state = await (await page.request.get(`${fake}/__state`)).json();
	expect(state.restored).toEqual(['api']);
	expect(state.images.api).toMatch(/backend:0\.57\.0@sha256:/);
	await shot(page, 'rolled-back', info.project.name);
	// The history says who did what.
	await expect(page.getByRole('region', { name: 'History' }).getByText('Rolled back').first()).toBeVisible();
});
