import { expect, test, type Page } from '@playwright/test';

// The Branding and Emails pages against a running panel with a stack
// config file (and, for previews, auth's internal email API). Screenshots
// of both pages in light and dark land in SCREENSHOT_DIR.
const dir = process.env.SCREENSHOT_DIR ?? 'test-results/screenshots';

const logo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="#7c3aed"/><path d="M18 46V18h16a9 9 0 0 1 0 18H24m10 0 10 10" fill="none" stroke="#fff" stroke-width="6" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
const wordmark = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 48" width="300" height="48"><rect width="48" height="48" rx="11" fill="#7c3aed"/><text x="60" y="34" font-family="Helvetica, Arial, sans-serif" font-size="28" font-weight="700" fill="#7c3aed">Example Chat</text></svg>`;

const theme = {
	version: 1,
	base: 'dark',
	colors: { accent: '#8b5cf6', 'accent-on': '#ffffff', bg: '#0b0b12', surface: '#11111b' },
	radius: 6,
	panes: { messages: { dim: 0.72, blur: 6, position: 'center' }, servers: { bg: '#08080d' } }
};

/** Calls the BFF from the page (same cookie and CSRF token as the UI). */
async function seed(page: Page) {
	await page.goto('/');
	await page.evaluate(
		async ({ logo, wordmark, theme }) => {
			const s = await (await fetch('/api/session')).json();
			const h = { 'X-CSRF-Token': s.csrf_token, 'X-Requested-With': 'e2e' };
			const put = async (slot: string, body: Blob) => {
				const r = await fetch(`/api/branding/images/${slot}`, {
					method: 'PUT',
					headers: { ...h, 'Content-Type': body.type },
					body
				});
				if (!r.ok) throw new Error(`${slot}: ${r.status} ${await r.text()}`);
			};
			// A raster (re-encoded to WebP by the BFF): a gradient with some shapes.
			const c = document.createElement('canvas');
			c.width = 960;
			c.height = 600;
			const g = c.getContext('2d')!;
			const grad = g.createLinearGradient(0, 0, 960, 600);
			grad.addColorStop(0, '#312e81');
			grad.addColorStop(0.5, '#7c3aed');
			grad.addColorStop(1, '#db2777');
			g.fillStyle = grad;
			g.fillRect(0, 0, 960, 600);
			for (let i = 0; i < 24; i++) {
				g.fillStyle = `rgba(255,255,255,${0.04 + (i % 5) * 0.03})`;
				g.beginPath();
				g.arc((i * 137) % 960, (i * 89) % 600, 30 + ((i * 53) % 120), 0, Math.PI * 2);
				g.fill();
			}
			const png = await new Promise<Blob>((res) => c.toBlob((b) => res(b!), 'image/png'));
			await put('logo', new Blob([logo], { type: 'image/svg+xml' }));
			await put('logo_wide', new Blob([wordmark], { type: 'image/svg+xml' }));
			await put('login_background', png);
			await put('panes.messages.image', png);
			const r = await fetch('/api/config', {
				method: 'PATCH',
				headers: { ...h, 'Content-Type': 'application/json' },
				body: JSON.stringify({
					set: {
						'branding.theme': theme,
						'auth.email.sender_name': 'Example Chat',
						'auth.email.accent': '#7c3aed',
						'auth.email.footer': 'Example Chat · example.com · You get this because you signed up.',
						'auth.email.logo': true
					}
				})
			});
			if (!r.ok) throw new Error(`config: ${r.status} ${await r.text()}`);
		},
		{ logo, wordmark, theme }
	);
}

async function setTheme(page: Page, mode: 'light' | 'dark') {
	await page.addInitScript((m) => localStorage.setItem('theme', m), mode);
}

/** Full-page screenshot with the viewport stretched to the page, so sticky bars sit where they belong. */
async function shoot(page: Page, path: string) {
	const vp = page.viewportSize()!;
	const h = await page.evaluate(() => document.documentElement.scrollHeight);
	await page.setViewportSize({ width: vp.width, height: Math.max(vp.height, h) });
	await page.waitForTimeout(150);
	await page.screenshot({ path });
	await page.setViewportSize(vp);
}

async function noHorizontalOverflow(page: Page) {
	const [scroll, width] = await page.evaluate(() => [
		document.documentElement.scrollWidth,
		document.documentElement.clientWidth
	]);
	expect(scroll).toBeLessThanOrEqual(width);
}

test.describe.configure({ mode: 'serial' });

test('seed a theme, images and email settings', async ({ page }) => {
	await seed(page);
});

for (const mode of ['dark', 'light'] as const) {
	test(`branding page (${mode})`, async ({ page }, info) => {
		await setTheme(page, mode);
		await page.goto('/branding');
		await expect(page.getByRole('heading', { name: 'Branding', level: 1 })).toBeVisible();
		const preview = page.getByRole('img', { name: /preview of the boson app/i });
		await expect(preview).toBeVisible();
		// The saved theme drives the preview's CSS variables.
		await expect
			.poll(() => preview.evaluate((el) => getComputedStyle(el.parentElement!).getPropertyValue('--accent')))
			.toBe('#8b5cf6');
		await expect(page.getByText('As saved')).toBeVisible();
		await expect(page.locator('.tile.active img')).toBeVisible();
		await noHorizontalOverflow(page);
		await shoot(page, `${dir}/branding-${info.project.name}-${mode}.png`);

		await page.getByRole('tab', { name: 'Sign-in screen' }).click();
		const login = page.getByRole('img', { name: /sign-in screen/i });
		await expect(login).toBeVisible();
		await expect(login.getByText('Sign in to Example Chat')).toBeVisible();
		await login.screenshot({ path: `${dir}/branding-signin-${info.project.name}-${mode}.png` });
	});
}

test('editing updates the preview and warns about contrast', async ({ page }) => {
	await page.goto('/branding');
	const text = page.getByRole('textbox', { name: /^Text --fg$/ });
	await text.fill('#141418');
	await text.blur();
	await expect(page.getByText('Unsaved preview')).toBeVisible();
	await expect(page.getByText(/Text on Background is .*:1/)).toBeVisible();
	await expect(page.getByText(/ignore this theme’s text colours/)).toBeVisible();
	await page.getByRole('button', { name: 'Discard' }).click();
	await expect(page.getByText('As saved')).toBeVisible();
	await expect(page.getByText(/Text on Background is/)).toBeHidden();
});

for (const mode of ['dark', 'light'] as const) {
	test(`emails page (${mode})`, async ({ page }, info) => {
		await setTheme(page, mode);
		await page.goto('/emails');
		await expect(page.getByRole('heading', { name: 'Emails', level: 1 })).toBeVisible();
		await expect(page.getByText('Branded layout')).toBeVisible();
		const frame = page.locator('iframe[title="Email preview"]');
		// Sandboxed with no scripts; same-origin only so the panel can fit its height.
		await expect(frame).toHaveAttribute('sandbox', 'allow-same-origin');
		// The logo is inlined as a data: URI (the sandboxed srcdoc can't load the panel's URLs).
		await expect(frame.contentFrame().locator('img')).toHaveAttribute('src', /^data:image\/svg\+xml;base64,/);
		await expect(frame.contentFrame().getByText(/Confirm your email address/)).toBeVisible();

		// Chips insert at the caret and the preview follows unsaved edits.
		const subject = page.getByRole('textbox', { name: 'Subject' });
		await subject.fill('Welcome to ');
		await page.getByRole('button', { name: '{{network}}' }).click();
		await expect(subject).toHaveValue('Welcome to {{network}}');
		await expect(page.getByText('Welcome to Example Chat')).toBeVisible();
		await noHorizontalOverflow(page);
		await shoot(page, `${dir}/emails-${info.project.name}-${mode}.png`);
	});
}
