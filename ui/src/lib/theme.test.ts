import { describe, expect, it } from 'vitest';
import {
	DAYLIGHT,
	TERMINAL,
	THEME_TOKENS,
	clean,
	contrast,
	contrastIssues,
	emptyTheme,
	imagesFor,
	nickColor,
	themeFor,
	variantFromBuiltin,
	resolve,
	sameTheme,
	themeVars,
	type NetworkTheme
} from './theme';

describe('tokens', () => {
	it('matches the client: 17 tokens, both built-ins complete', () => {
		expect(THEME_TOKENS.map((t) => t.key)).toEqual([
			'bg',
			'surface',
			'surface-raised',
			'surface-hover',
			'border',
			'border-soft',
			'fg',
			'fg-2',
			'fg-3',
			'muted',
			'meta',
			'accent',
			'accent-on',
			'success',
			'warn',
			'danger',
			'info'
		]);
		for (const b of [TERMINAL, DAYLIGHT])
			for (const t of THEME_TOKENS) expect(b.colors[t.key]).toMatch(/^#[0-9A-F]{6}$/);
	});
});

describe('resolve', () => {
	it('fills missing tokens from the base built-in', () => {
		const c = resolve({ version: 1, base: 'light', colors: { accent: '#7C3AED' } });
		expect(c.accent).toBe('#7c3aed');
		expect(c.bg).toBe(DAYLIGHT.colors.bg.toLowerCase());
		expect(resolve(emptyTheme()).fg).toBe(TERMINAL.colors.fg.toLowerCase());
	});
	it('ignores invalid colours', () => {
		const c = resolve({ version: 1, base: 'dark', colors: { bg: 'red' } });
		expect(c.bg).toBe('#070709');
	});
});

describe('clean', () => {
	it('drops empties and clamps numbers', () => {
		const t = clean({
			version: 1,
			base: 'dark',
			colors: { accent: '#ABCDEF', fg: '' },
			radius: 40,
			panes: {
				messages: { dim: 1.7, blur: 99, position: 'top' },
				members: {},
				servers: { bg: 'nope' }
			}
		} as NetworkTheme);
		expect(t).toEqual({
			version: 1,
			base: 'dark',
			colors: { accent: '#abcdef' },
			radius: 12,
			panes: { messages: { dim: 1, blur: 24, position: 'top' } }
		});
	});
	it('sameTheme ignores key order and empties', () => {
		expect(
			sameTheme(
				{ version: 1, base: 'dark', colors: { accent: '#aaaaaa', bg: '#000000' }, panes: { members: {} } },
				{ version: 1, base: 'dark', colors: { bg: '#000000', accent: '#AAAAAA' } }
			)
		).toBe(true);
		expect(sameTheme(emptyTheme('dark'), emptyTheme('light'))).toBe(false);
	});
});

describe('contrast', () => {
	it('computes WCAG ratios', () => {
		expect(contrast('#000000', '#ffffff')).toBeCloseTo(21, 5);
		expect(contrast('#777777', '#ffffff')).toBeCloseTo(4.48, 2);
		expect(contrast('#ffffff', '#ffffff')).toBe(1);
	});
	it('the built-ins pass', () => {
		expect(contrastIssues(emptyTheme('dark'))).toEqual([]);
		expect(contrastIssues(emptyTheme('light'))).toEqual([]);
	});
	it('flags text on background with the client guard, and pane colours', () => {
		const issues = contrastIssues({
			version: 1,
			base: 'dark',
			colors: { fg: '#222222' },
			panes: { channels: { bg: '#2a2a2a' } }
		});
		const main = issues.find((i) => i.fg === 'fg' && i.bg === 'bg');
		expect(main?.guard).toBe(true);
		expect(main?.ratio).toBeLessThan(4.5);
		expect(issues.some((i) => i.bg === 'pane-channels-bg' && !i.guard)).toBe(true);
	});
	it('flags text on the accent', () => {
		const issues = contrastIssues({
			version: 1,
			base: 'dark',
			colors: { accent: '#8b5cf6', 'accent-on': '#ffffff' }
		});
		expect(issues.map((i) => i.label)).toEqual(['Text on accent buttons']);
	});
});

describe('themeVars', () => {
	it('sets tokens, radius steps, fonts and pane variables', () => {
		const v = themeVars(
			{
				version: 1,
				base: 'dark',
				radius: 4,
				font: 'system',
				panes: { messages: { dim: 0.7, blur: 8, position: 'top' }, servers: { bg: '#010203' } }
			},
			{ 'panes.messages.image': '/api/branding/assets/pane-messages-abc.webp' }
		);
		expect(v['--accent']).toBe('#d97706');
		expect(v['--radius-sm']).toBe('4px');
		expect(v['--radius-lg']).toBe('12px');
		expect(v['--font-body']).toMatch(/system-ui/);
		expect(v['--pane-messages-image']).toBe('url("/api/branding/assets/pane-messages-abc.webp")');
		expect(v['--pane-messages-dim']).toBe('0.7');
		expect(v['--pane-messages-blur']).toBe('8px');
		expect(v['--pane-messages-position']).toBe('center top');
		expect(v['--pane-servers-bg']).toBe('#010203');
		expect(v['--pane-channels-bg']).toBe('#0d0d11');
		// no image: no dim layer
		expect(v['--pane-channels-image']).toBe('none');
		expect(v['--pane-channels-dim']).toBe('0');
	});
	it('can’t be broken out of url()', () => {
		const v = themeVars(emptyTheme(), { 'panes.servers.image': '/x".png") ; color: red' });
		expect(v['--pane-servers-image']).toBe('url("/x.png) ; color: red")');
	});
	it('nick colours follow the theme', () => {
		expect(nickColor('ada', { version: 1, base: 'light' })).toMatch(/60% 38%\)$/);
		expect(nickColor('ada', emptyTheme())).toBe(nickColor('ada', emptyTheme()));
	});
});

describe('variants', () => {
	const t: NetworkTheme = {
		version: 1,
		base: 'dark',
		colors: { accent: '#8b5cf6', bg: '#0b0b12' },
		radius: 6,
		font: 'system',
		panes: { messages: { dim: 0.7, blur: 6, bg: '#000000' } },
		variants: { light: { colors: { accent: '#6D28D9' }, panes: { messages: { dim: 0.3 } } } }
	};

	it('clean keeps only the other base, empty variants included', () => {
		expect(clean(t).variants).toEqual({
			light: { colors: { accent: '#6d28d9' }, panes: { messages: { dim: 0.3 } } }
		});
		expect(clean({ ...t, variants: { dark: { colors: { bg: '#ffffff' } } } }).variants).toBeUndefined();
		expect(clean({ version: 1, base: 'light', variants: { dark: {} } }).variants).toEqual({ dark: {} });
		expect(sameTheme(t, { ...t, variants: undefined })).toBe(false);
	});

	it('themeFor picks the variant for its mode and overlays panes', () => {
		expect(themeFor(t, 'dark').variants).toBeUndefined();
		expect(themeFor(t, 'dark').colors?.accent).toBe('#8b5cf6');
		const light = themeFor(t, 'light');
		expect(light.base).toBe('light');
		expect(light.colors).toEqual({ accent: '#6D28D9' });
		expect(light.radius).toBe(6);
		expect(light.font).toBe('system');
		expect(light.panes?.messages).toEqual({ dim: 0.3, blur: 6, bg: '#000000' });
		expect(resolve(light).bg).toBe(DAYLIGHT.colors.bg.toLowerCase());
		// No variant: the primary everywhere.
		expect(themeFor({ ...t, variants: undefined }, 'light').base).toBe('dark');
	});

	it('imagesFor uses a variant pane image over the primary one', () => {
		const imgs = {
			'panes.messages.image': '/a.webp',
			'variants.light.panes.messages.image': '/b.webp'
		} as const;
		expect(imagesFor(t, 'light', imgs)['panes.messages.image']).toBe('/b.webp');
		expect(imagesFor(t, 'dark', imgs)['panes.messages.image']).toBe('/a.webp');
		expect(imagesFor(t, 'light', { 'panes.messages.image': '/a.webp' })['panes.messages.image']).toBe(
			'/a.webp'
		);
	});

	it('starts a variant from Boson’s built-in', () => {
		const v = variantFromBuiltin('light');
		expect(v.colors?.bg).toBe(DAYLIGHT.colors.bg.toLowerCase());
		expect(v.nick).toEqual(DAYLIGHT.nick);
		expect(contrastIssues(themeFor({ version: 1, base: 'dark', variants: { light: v } }, 'light'))).toEqual(
			[]
		);
	});
});
