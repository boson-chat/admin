import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';

// The Federation page against the panel on the fake auth with federation
// (FAKE_AUTH_ROLES=1 FAKE_AUTH_FEDERATION=1), which also serves boson.json:
//
//   ./scripts/e2e-federation.sh  # SCREENSHOT_DIR=... keeps screenshots
const fake = process.env.FAKE_AUTH_INTERNAL_URL ?? '';
const configFile = process.env.BOSON_CONFIG_FILE ?? '';
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

test.skip(
	!fake,
	'set FAKE_AUTH_INTERNAL_URL (the panel must run on FAKE_AUTH_FEDERATION=1; scripts/e2e-federation.sh)'
);

const KEYS = ['mode', 'allow', 'block', 'directory_url', 'remote_upload_quota', 'remote_voice'].map(
	(k) => `auth.federation.${k}`
);

async function noHorizontalOverflow(page: Page) {
	const [scroll, width] = await page.evaluate(() => [
		document.documentElement.scrollWidth,
		document.documentElement.clientWidth
	]);
	expect(scroll).toBeLessThanOrEqual(width);
}

async function shoot(page: Page, name: string) {
	await noHorizontalOverflow(page);
	await page.waitForTimeout(200);
	const vp = page.viewportSize()!;
	const h = await page.evaluate(() => document.documentElement.scrollHeight);
	await page.setViewportSize({ width: vp.width, height: Math.max(vp.height, h) });
	await page.waitForTimeout(200);
	await page.screenshot({ path: `${dir}/${name}.png` });
	await page.setViewportSize(vp);
}

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

async function audit(
	page: Page
): Promise<{ event: string; identity?: string; domain?: string; reason?: string }[]> {
	return (await (await page.request.get(`${fake}/__audit`)).json()).audit;
}

const file = () => readFileSync(configFile, 'utf8');

test.beforeEach(async ({ page }) => {
	expect((await page.request.post(`${fake}/__reset`)).ok()).toBe(true);
	await patchConfig(page, { unset: KEYS });
	// The panel caches the boson.json check for a minute.
	expect(await page.evaluate(async () => (await fetch('/api/federation?fresh=1')).status)).toBe(200);
});

test('the page: modes with their tradeoffs, home status, remote users', async ({ page }, info) => {
	await page.goto('/federation');
	await expect(page.getByRole('heading', { name: 'Federation', level: 1 })).toBeVisible();
	// It's in the navigation, under Accounts.
	if (info.project.name === 'desktop')
		await expect(page.getByRole('link', { name: 'Federation', exact: true })).toBeVisible();

	// Verified servers is the default, with its tradeoff spelled out.
	const verified = page.getByRole('radio', { name: /Verified servers/ });
	await expect(verified).toBeChecked();
	await expect(page.getByTestId('mode-risk')).toContainText('A verified listing proves who runs a domain');
	await page.getByText('Open', { exact: true }).click();
	await expect(page.getByTestId('mode-risk')).toContainText('spam and ban evasion');
	await verified.check();

	// This server as a home: published, every check green, a link to test.
	const home = page.getByTestId('home-status');
	await expect(home.getByText('Published', { exact: true })).toBeVisible();
	await expect(home.getByText('Its issuer is this auth service')).toBeVisible();
	await expect(home.getByTestId('home-link')).toHaveAttribute(
		'href',
		'https://example.org/.well-known/boson.json'
	);

	// Remote users: identity, handle, banned ones marked.
	const alice = page.getByTestId('remote-usr_fed_alice');
	await expect(alice).toContainText('alice@boson.chat');
	await expect(page.getByTestId('remote-usr_fed_kai').getByText('Banned', { exact: true })).toBeVisible();
	await expect(page.getByText('4 remote users')).toBeVisible();
	await shoot(page, `federation-${info.project.name}`);

	// Search.
	await page.getByRole('searchbox', { name: 'Search remote users' }).fill('chatexample');
	await expect(page.getByText('1 remote user')).toBeVisible();
	await expect(page.getByTestId('remote-usr_fed_alice2')).toBeVisible();
});

test('allowlist mode needs a server; saved to the config file', async ({ page }, info) => {
	await page.goto('/federation');
	await page.getByText('Allowlist', { exact: true }).first().click();
	await expect(page.getByText(/an empty allowlist lets nobody in/)).toBeVisible();
	await expect(page.getByRole('button', { name: 'Save changes' })).toBeDisabled();

	const add = page.getByRole('textbox', { name: 'Add to the allowlist' });
	await add.fill('not a domain');
	await expect(page.getByText(/isn’t a domain/)).toBeVisible();
	await add.fill('https://Boson.Chat/');
	await add.press('Enter');
	await expect(page.getByRole('list', { name: 'allowlist' }).getByText('boson.chat')).toBeVisible();

	await page.getByLabel('Upload quota').fill('750MB');
	await page.getByText('Remote users can join voice').click();
	if (info.project.name === 'mobile') await shoot(page, 'federation-allowlist-mobile');
	await page.getByRole('button', { name: 'Save changes' }).click();
	await expect(page.getByText('Federation saved.')).toBeVisible();

	const f = file();
	expect(f).toContain('mode: allowlist');
	expect(f).toMatch(/allow:\s*\n\s*- boson\.chat/);
	expect(f).toContain('remote_upload_quota: 750MB');
	expect(f).toContain('remote_voice: false');
	await expect(page.getByText('Set in config/boson.yaml').first()).toBeVisible();

	// The home check notices boson.json still advertises the old mode.
	await page.getByRole('button', { name: 'Check again' }).click();
	await expect(page.getByTestId('home-status').getByText('It advertises this server’s mode')).toBeVisible();
	await expect(page.getByTestId('home-status').getByText(/Restart the wellknown service/)).toBeVisible();

	// Reset one back to the default.
	await page.getByRole('button', { name: 'Reset to default' }).last().click();
	await expect(page.getByText(/is back to its default/)).toBeVisible();
});

test('ban and unban a remote user, logged', async ({ page }) => {
	await page.goto('/federation');
	const bob = page.getByTestId('remote-usr_fed_bob');
	await bob.getByRole('button', { name: 'Ban' }).click();
	const dlg = page.getByRole('dialog', { name: 'Ban bob@chat.example.net?' });
	await expect(dlg).toContainText('dropped from IRC');
	await expect(dlg).toContainText('Other people from chat.example.net aren’t affected');
	await dlg.getByLabel(/Reason/).fill('flooding #lobby');
	await dlg.getByRole('button', { name: 'Ban' }).click();
	await expect(bob.getByText('Banned', { exact: true })).toBeVisible();
	expect(await audit(page)).toContainEqual(
		expect.objectContaining({
			event: 'federation_identity_banned',
			identity: 'bob@chat.example.net',
			reason: 'flooding #lobby'
		})
	);

	await bob.getByRole('button', { name: 'Unban' }).click();
	await page.getByRole('dialog', { name: /Unban/ }).getByRole('button', { name: 'Unban' }).click();
	await expect(bob.getByText('Banned', { exact: true })).toHaveCount(0);
	expect((await audit(page)).map((e) => e.event)).toContain('federation_identity_unbanned');
});

test('block a whole home server: confirm says it disconnects them', async ({ page }, info) => {
	await page.goto('/federation');
	await page.getByTestId('remote-usr_fed_bob').getByRole('button', { name: 'Block server' }).click();
	const dlg = page.getByRole('dialog', { name: 'Block chat.example.net?' });
	await expect(dlg).toContainText('Nobody from chat.example.net can sign in here while it’s blocked');
	// auth ≥ 1.26.0 (the fake's version): a block drops them from IRC too.
	await expect(dlg).toContainText('dropped from IRC within seconds');
	await expect(dlg).not.toContainText('stays connected');
	await expect(dlg).toContainText('Unblocking lets them sign in again. Nothing is deleted');
	await expect(dlg).toContainText('2 people');
	if (info.project.name === 'desktop') await shoot(page, 'federation-block-confirm-desktop');
	await dlg.getByRole('button', { name: 'Block and disconnect' }).click();
	await expect(page.getByText('chat.example.net is blocked. 2 sessions ended.')).toBeVisible();
	await expect(
		page.getByTestId('remote-usr_fed_alice2').getByText('Server blocked', { exact: true })
	).toBeVisible();
	await expect(page.getByRole('list', { name: 'blocklist' }).getByText('chat.example.net')).toBeVisible();
	expect(file()).toMatch(/block:\s*\n\s*- chat\.example\.net/);
	expect(await audit(page)).toContainEqual(
		expect.objectContaining({ event: 'federation_domain_sessions_revoked', domain: 'chat.example.net' })
	);

	// auth applies the file's block a moment later (the fake: 2 s).
	await expect(page.getByTestId('applying')).toHaveCount(0, { timeout: 15_000 });
	await expect(
		page.getByTestId('remote-usr_fed_bob').getByText('Server blocked', { exact: true })
	).toBeVisible();

	// Unblock from the row.
	await page
		.getByTestId('remote-usr_fed_bob')
		.getByRole('button', { name: 'Unblock chat.example.net' })
		.click();
	await expect(page.getByText('chat.example.net is off the blocklist.')).toBeVisible();
	// The row follows the panel's own write at once, before auth re-reads
	// the file, and says it's applying; then auth catches up.
	const bob = page.getByTestId('remote-usr_fed_bob');
	await expect(page.getByText('Server blocked', { exact: true })).toHaveCount(0);
	await expect(bob.getByTestId('applying')).toBeVisible();
	if (info.project.name === 'desktop') await shoot(page, 'federation-unblock-applying-desktop');
	await expect(bob.getByTestId('applying')).toHaveCount(0, { timeout: 15_000 });
	await expect(page.getByText('Server blocked', { exact: true })).toHaveCount(0);
});

test('a block added in the form asks first and signs them out on save', async ({ page }) => {
	await page.goto('/federation');
	const add = page.getByRole('textbox', { name: 'Add to the blocklist' });
	await add.fill('alice@boson.chat');
	await add.press('Enter');
	await page.getByRole('button', { name: 'Save changes' }).click();
	const dlg = page.getByRole('dialog', { name: 'Block boson.chat?' });
	await expect(dlg).toContainText('Nobody from boson.chat can sign in here');
	await expect(dlg).toContainText('dropped from IRC within seconds');
	await dlg.getByRole('button', { name: 'Save and disconnect them' }).click();
	await expect(page.getByText(/Federation saved\. 2 sessions ended\./)).toBeVisible();
	await expect(
		page.getByTestId('remote-usr_fed_alice').getByText('Server blocked', { exact: true })
	).toBeVisible();
});

test('a home server without the federation block says so', async ({ page }, info) => {
	await page.request.post(`${fake}/__home`, { data: { state: 'missing' } });
	await page.goto('/federation');
	await page.getByRole('button', { name: 'Check again' }).click();
	const home = page.getByTestId('home-status');
	await expect(home.getByText('Not published', { exact: true })).toBeVisible();
	await expect(home.getByText(/other servers can’t verify your users/)).toBeVisible();
	if (info.project.name === 'mobile') await shoot(page, 'federation-home-missing-mobile');
});
