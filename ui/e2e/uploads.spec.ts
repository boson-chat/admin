import { expect, test, type Page } from '@playwright/test';

// The Uploads, Uploaded files and user usage views against the panel
// running on the fake auth and the fake files service (e2e/fake-files.mjs),
// which reads the files: section back from the config file:
//
//   ./scripts/e2e-files.sh     # starts all three; SCREENSHOT_DIR=... keeps screenshots
const fake = process.env.FAKE_FILES_URL ?? '';
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

test.skip(!fake, 'set FAKE_FILES_URL (the panel must run on e2e/fake-files.mjs; scripts/e2e-files.sh)');

const KEYS = [
	'files.enabled',
	'files.retention',
	'files.window',
	'files.account.quota',
	'files.account.max_file',
	'files.guest.enabled',
	'files.guest.quota',
	'files.guest.max_file',
	'files.storage.driver',
	'files.storage.s3.endpoint',
	'files.storage.s3.bucket',
	'files.storage.s3.region',
	'files.storage.s3.path_style',
	'files.storage.s3.public_base_url'
];

async function fakeState(page: Page, s: Record<string, unknown>) {
	const r = await page.request.post(`${fake}/__state`, { data: s });
	expect(r.ok()).toBe(true);
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

async function open(page: Page) {
	await page.goto('/uploads');
	await expect(page.getByRole('heading', { name: 'Uploads', level: 1 })).toBeVisible();
	await expect(page.getByRole('textbox', { name: 'Quota', exact: true })).toBeVisible();
}

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

test.beforeEach(async ({ page }) => {
	await fakeState(page, { reset: true });
	await patchConfig(page, { unset: KEYS });
});

test('saves human units, and the files service picks them up', async ({ page }) => {
	await open(page);
	await expect(page.getByTestId('storage-state')).toContainText('Healthy');
	await page.getByText('Let people upload files', { exact: true }).click();
	await expect(page.getByRole('switch', { name: /Let people upload files/ })).toBeChecked();
	await page.getByRole('textbox', { name: 'Keep files for' }).fill('2d');
	await expect(page.getByText('= 2 days')).toBeVisible();
	await page.getByRole('textbox', { name: 'Quota', exact: true }).fill('10 GB');
	await page.getByRole('textbox', { name: 'Largest file', exact: true }).fill('20GB');
	await expect(page.getByText('Can’t be bigger than the quota.')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Save changes' })).toBeDisabled();
	await page.getByRole('textbox', { name: 'Largest file', exact: true }).fill('1.5gb');
	await expect(page.getByText('= 1.5 GB')).toBeVisible();
	await page.getByRole('button', { name: 'Save changes' }).click();
	await expect(page.getByText(/Uploads saved/)).toBeVisible();
	// The fake reads the file back: the status echoes it.
	await page.getByRole('button', { name: 'Refresh' }).click();
	const echo = page.locator('dl.echo');
	await expect(echo).toContainText('2 days');
	await expect(echo).toContainText('10 GB / 3 days, ≤ 1.5 GB each');
	await expect(page.getByRole('textbox', { name: 'Keep files for' })).toHaveValue('48h');
});

test('S3 storage needs an endpoint and a bucket; keys stay in .env', async ({ page }) => {
	await open(page);
	await page.getByText('S3-compatible', { exact: true }).click();
	await expect(page.getByTestId('s3-fields')).toBeVisible();
	await expect(page.getByText('Needed for S3 storage.')).toHaveCount(2);
	await expect(page.getByText('FILES_S3_ACCESS_KEY')).toBeVisible();
	await page.getByRole('textbox', { name: 'Endpoint' }).fill('http://minio:9000');
	await page.getByRole('textbox', { name: 'Bucket' }).fill('boson-files');
	await page.getByRole('button', { name: 'Save changes' }).click();
	await expect(page.getByText(/Uploads saved/)).toBeVisible();
	await page.getByRole('button', { name: 'Refresh' }).click();
	await expect(page.locator('aside')).toContainText('boson-files');
});

test('shows a failing storage', async ({ page }) => {
	await fakeState(page, { storage_ok: false });
	await open(page);
	await expect(page.getByTestId('storage-state')).toContainText('Failing');
	await expect(page.locator('.problems')).toContainText('no space left on device');
});

test('a wrong internal token says what to match', async ({ page }) => {
	await fakeState(page, { token_ok: false });
	await open(page);
	await expect(page.getByText(/Couldn’t read the files service’s status/)).toContainText(
		'BOSON_FILES_INTERNAL_TOKEN'
	);
});

test('moderation: search, filter by uploader, delete', async ({ page }) => {
	await page.goto('/files');
	await expect(page.getByRole('heading', { name: 'Uploaded files', level: 1 })).toBeVisible();
	await expect(page.locator('tbody tr')).toHaveCount(5);
	await page.getByRole('searchbox', { name: 'Search file names' }).fill('notes');
	await page.getByRole('button', { name: 'Search' }).click();
	await expect(page.locator('tbody tr')).toHaveCount(1);
	await page.getByRole('searchbox', { name: 'Search file names' }).fill('');
	await page.getByRole('button', { name: 'Search' }).click();
	await expect(page.locator('tbody tr')).toHaveCount(5);

	const row = page.locator('tbody tr').filter({ hasText: 'clip.mp4' });
	await expect(row).toContainText('Guest');
	await row.getByRole('button', { name: 'Delete' }).click();
	const dlg = page.getByRole('dialog');
	await expect(dlg).toContainText('keeps counting');
	await dlg.getByRole('textbox').fill('spam');
	await dlg.getByRole('button', { name: 'Delete file' }).click();
	await expect(row).toContainText('Deleted');
	await expect(row.getByRole('button', { name: 'Delete' })).toHaveCount(0);

	await page.goto('/files?user=usr_admin&name=admin');
	await expect(page.getByTestId('user-filter')).toContainText('admin');
	await expect(page.locator('tbody tr')).toHaveCount(3);
});

test('a user’s page shows their usage', async ({ page }) => {
	await page.goto('/users/usr_admin');
	const u = page.getByTestId('files-usage');
	await expect(u).toContainText('of 5 GB');
	await expect(u).toContainText('1 GB frees in 20 h');
	await page.getByRole('link', { name: 'Their files' }).click();
	await expect(page).toHaveURL(/\/files\?user=usr_admin/);
});

for (const mode of ['light', 'dark'] as const) {
	test(`screenshots (${mode})`, async ({ page }, info) => {
		await patchConfig(page, {
			set: { 'files.enabled': true, 'files.account.quota': '5GB', 'files.guest.quota': '500MB' }
		});
		await page.addInitScript((m) => localStorage.setItem('theme', m), mode);
		await open(page);
		await expect(page.getByTestId('storage-state')).toContainText('Healthy');
		await shoot(page, `uploads-${info.project.name}-${mode}`);

		await page.getByText('S3-compatible', { exact: true }).click();
		await page.getByRole('textbox', { name: 'Endpoint' }).fill('http://minio:9000');
		await shoot(page, `uploads-s3-${info.project.name}-${mode}`);

		await page.goto('/files');
		await expect(page.locator('tbody tr')).toHaveCount(5);
		await shoot(page, `files-${info.project.name}-${mode}`);

		await page.goto('/users/usr_admin');
		await expect(page.getByTestId('files-usage')).toBeVisible();
		await page.getByTestId('files-usage').scrollIntoViewIfNeeded();
		await shoot(page, `user-usage-${info.project.name}-${mode}`);
	});
}
