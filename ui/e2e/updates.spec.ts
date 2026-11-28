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
	await expect(page.getByText('4 parts of this install have a newer release.')).toBeVisible({
		timeout: 10_000
	});
	await expect(page.getByRole('article', { name: /Auth/ })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Up to date' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Check now' })).toBeEnabled();
	await page.unrouteAll({ behavior: 'ignoreErrors' });
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
	await expect(page.getByText('4 parts of this install have a newer release.')).toBeVisible();
	await page.unrouteAll({ behavior: 'ignoreErrors' });
});

test('update available → changelog across versions → apply → checks pass', async ({ page }, info) => {
	await fresh(page);
	await expect(page.getByRole('heading', { name: 'Updates', level: 1 })).toBeVisible();
	await expect(page.getByText('4 parts of this install have a newer release.')).toBeVisible();
	// The nav badge (behind the menu on a phone).
	const menu = page.getByRole('button', { name: 'Menu' });
	if (await menu.isVisible()) await menu.click();
	await expect(page.getByRole('link', { name: /Updates\s*4/ })).toBeVisible();
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
	// Now the api and the stack are left.
	await expect(page.getByText('2 parts of this install have a newer release.')).toBeVisible();
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

// Docker out of reach when a rollback starts (as right after compose
// replaced the socket proxies): the rollback says plainly that it didn't
// run and changed nothing, can be tried again once Docker answers, and the
// check's error goes with the next check that reaches Docker.
test('a rollback that can’t reach Docker → didn’t run, nothing changed → again once it answers', async ({
	page
}, info) => {
	await fresh(page);
	await page.getByRole('checkbox', { name: 'Select API' }).check();
	await page.getByRole('button', { name: 'Update selected (1)' }).click();
	const dlg = page.getByRole('dialog');
	await dlg.getByLabel(/I’ve read these/).check();
	await dlg.getByRole('button', { name: 'Back up and update' }).click();
	await expect(page.locator('.run .tag', { hasText: 'Failed' })).toBeVisible({ timeout: 20_000 });

	const down = (seconds: number) =>
		page.request.post(`${fake}/__state`, { data: { proxy_down_for: seconds } });
	expect((await down(3600)).ok()).toBe(true);
	await page.getByRole('button', { name: 'Roll back' }).click();
	const rb = page.getByRole('dialog', { name: 'Roll back' });
	await rb.getByRole('button', { name: 'Roll back' }).click();
	await expect(page.locator('.run .tag', { hasText: 'Didn’t run' })).toBeVisible({ timeout: 20_000 });
	await expect(
		page.getByText(
			"Rollback didn't run: Docker was unreachable. Nothing was changed; API 0.58.0 is installed."
		)
	).toBeVisible();
	await expect(page.getByRole('heading', { name: /Rollback/ })).toBeVisible();
	let state = await (await page.request.get(`${fake}/__state`)).json();
	expect(state.restored ?? []).toEqual([]);
	expect(state.images.api).toBe('harbor.floret.dev/boson/backend:0.58.0');
	await shot(page, 'rollback-didnt-run', info.project.name);

	expect((await down(0)).ok()).toBe(true);
	await page.getByRole('button', { name: 'Try the rollback again' }).click();
	await rb.getByRole('button', { name: 'Roll back' }).click();
	await expect(page.locator('.run .tag', { hasText: 'Rolled back' })).toBeVisible({ timeout: 20_000 });
	state = await (await page.request.get(`${fake}/__state`)).json();
	expect(state.images.api).toMatch(/backend:0\.57\.0@sha256:/);
	// The banner from the check that couldn't reach Docker goes with the next one.
	await page.getByRole('button', { name: 'Check now' }).click();
	await expect(page.getByRole('button', { name: 'Check now' })).toBeEnabled();
	await expect(page.getByText(/docker_unavailable/)).toHaveCount(0);
});

// The stack: what release 1.1.0 changes on this install (files with their
// diffs, a local edit, Ergo included on the stack's version, auth's pin
// raised, a migration, what restarts and who notices), then apply → done,
// Ergo and Traefik on their new images, and the stack on 1.1.0.
test('stack release → what changes (files, images, pins, migrations) → apply → done', async ({
	page
}, info) => {
	await fresh(page);
	const stack = page.getByRole('article', { name: /Stack/ });
	await expect(stack.getByText('pre-release')).toBeVisible();
	await expect(stack.locator('.ver strong')).toHaveText('1.1.0');
	await expect(stack.getByText('disconnects chat')).toBeVisible();
	await expect(stack.getByText(/Everyone on IRC is disconnected briefly \(Ergo restarts\)/)).toBeVisible();
	await expect(stack.getByText(/from before stack releases/)).toBeVisible();
	await expect(stack.getByText(/traefik\/dynamic\/stack\.yml\. The update replaces it/)).toBeVisible();
	await expect(stack.locator('.facts')).toContainText('auth, ergo, roleserv, traefik');
	await expect(stack.getByText('stack-ergo:2.19.1 → stack-ergo:1.1.0').first()).toBeVisible();
	await expect(stack.getByText('auth:1.20.0 → auth:1.21.0').first()).toBeVisible();
	await expect(stack.getByText(/newer than the release’s default: kept/)).toBeVisible();
	await expect(stack.locator('ul.rows li', { hasText: '0001-bridge-client-webirc.sh' })).toContainText(
		'WEBIRC'
	);
	await stack.getByText(/^Files: 5 changed/).click();
	await stack.locator('.file summary', { hasText: 'compose.yaml' }).click();
	await expect(stack.locator('.diff .dadd', { hasText: 'traefik:v3.7.20' })).toBeVisible();
	await expect(stack.locator('.file', { hasText: 'traefik/dynamic/stack.yml' }).locator('.tag')).toHaveText(
		'edited here: replaced, kept as .local'
	);
	// Long diff lines scroll in their box; the page never scrolls sideways.
	expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
	await shot(page, 'stack-review', info.project.name);

	await page.getByRole('checkbox', { name: 'Select Stack' }).check();
	await page.getByRole('button', { name: 'Update selected (1)' }).click();
	const dlg = page.getByRole('dialog');
	await expect(dlg.getByText(/disconnect all chat users/).first()).toBeVisible();
	await expect(dlg.getByText('pre-release → 1.1.0')).toBeVisible();
	await expect(dlg.getByText(/Replaces traefik\/dynamic\/stack\.yml/)).toBeVisible();
	await expect(dlg.getByText(/rolls itself back/)).toBeVisible();
	// 1.0.0 → 1.1.0 isn't a major version; the pre-release jump isn't either.
	await expect(dlg.getByText('Breaking changes.')).toHaveCount(0);
	await shot(page, 'stack-confirm', info.project.name);
	await dlg.getByRole('button', { name: 'Back up and update' }).click();

	await expect(page.locator('.run .tag', { hasText: 'Done' })).toBeVisible({ timeout: 20_000 });
	const run = page.locator('.run');
	await expect(run.locator('.step', { hasText: 'Download the stack release' })).toContainText('SHA-256');
	await expect(run.locator('.step', { hasText: 'Write the stack files' })).toContainText('1 migrations run');
	await expect(run.locator('.checks li', { hasText: 'Stack release' })).toContainText('VERSION 1.1.0');
	await expect(run.locator('.checks li', { hasText: 'ergo image' })).toContainText('stack-ergo:1.1.0');
	await expect(page.locator('.checks li.bad')).toHaveCount(0);
	await expect(run.getByText(/recreated auth, ergo, roleserv, traefik/)).toBeVisible();
	const state = await (await page.request.get(`${fake}/__state`)).json();
	expect(state.images.ergo).toBe('harbor.floret.dev/boson/stack-ergo:1.1.0');
	expect(state.images.traefik).toBe('traefik:v3.7.20');
	expect(state.images.quicvoiced).toBe('harbor.floret.dev/boson/quicvoice:1.15.0');
	expect(state.env.AUTH_IMAGE).toBe('harbor.floret.dev/boson/auth:1.21.0');
	await shot(page, 'stack-done', info.project.name);
	await expect(page.locator('.uptodate tr', { hasText: 'compose files, Ergo' })).toContainText('1.1.0');
});
