/**
 * boson-theme/1, the network theme the stack, the directory and the client
 * share (branding contract, section 1). The token list and the two
 * built-ins are the client's (client/src/renderer/src/modules/ui/theme/
 * theme.types.ts and themes.builtin.ts); keep them in step.
 */

export const THEME_TOKENS = [
	{ key: 'bg', label: 'Background', group: 'Surfaces' },
	{ key: 'surface', label: 'Panels', group: 'Surfaces' },
	{ key: 'surface-raised', label: 'Raised panels', group: 'Surfaces' },
	{ key: 'surface-hover', label: 'Hover', group: 'Surfaces' },
	{ key: 'border', label: 'Borders', group: 'Surfaces' },
	{ key: 'border-soft', label: 'Soft borders', group: 'Surfaces' },
	{ key: 'fg', label: 'Text', group: 'Text' },
	{ key: 'fg-2', label: 'Secondary text', group: 'Text' },
	{ key: 'fg-3', label: 'Tertiary text', group: 'Text' },
	{ key: 'muted', label: 'Muted', group: 'Text' },
	{ key: 'meta', label: 'Faint', group: 'Text' },
	{ key: 'accent', label: 'Accent', group: 'Accent' },
	{ key: 'accent-on', label: 'Text on accent', group: 'Accent' },
	{ key: 'success', label: 'Success', group: 'Status' },
	{ key: 'warn', label: 'Warning', group: 'Status' },
	{ key: 'danger', label: 'Danger', group: 'Status' },
	{ key: 'info', label: 'Info', group: 'Status' }
] as const;

export type ThemeToken = (typeof THEME_TOKENS)[number]['key'];
export type ThemeColors = Record<ThemeToken, string>;
export type ThemeBase = 'dark' | 'light';
export type ThemeFont = 'boson' | 'system';
export const TOKEN_GROUPS = ['Surfaces', 'Text', 'Accent', 'Status'] as const;

export const MAX_RADIUS = 12;
export const MAX_BLUR = 24;
export const PANES = ['servers', 'channels', 'messages', 'members'] as const;
export type PaneName = (typeof PANES)[number];
export const PANE_LABELS: Record<PaneName, string> = {
	servers: 'Server rail',
	channels: 'Channel list',
	messages: 'Messages',
	members: 'Member list'
};
export const POSITIONS = ['center', 'top', 'bottom', 'left', 'right'] as const;
export type PanePosition = (typeof POSITIONS)[number];

export interface Pane {
	bg?: string;
	dim?: number;
	blur?: number;
	position?: PanePosition;
}

/** The document as stored in branding.theme (no image URLs). */
export interface NetworkTheme {
	version: 1;
	base: ThemeBase;
	colors?: Partial<ThemeColors>;
	nick?: { saturation: number; lightness: number };
	radius?: number;
	font?: ThemeFont;
	panes?: Partial<Record<PaneName, Pane>>;
}

/** branding.images keys. */
export const IMAGE_SLOTS = [
	'logo',
	'logo_wide',
	'login_background',
	'panes.servers.image',
	'panes.channels.image',
	'panes.messages.image',
	'panes.members.image'
] as const;
export type ImageSlot = (typeof IMAGE_SLOTS)[number];
export const paneSlot = (p: PaneName) => `panes.${p}.image` as ImageSlot;

interface Builtin {
	name: string;
	colors: ThemeColors;
	nick: { saturation: number; lightness: number };
	radius: number;
	font: ThemeFont;
}

/** Terminal: Boson's stock dark look. */
export const TERMINAL: Builtin = {
	name: 'Terminal',
	colors: {
		bg: '#070709',
		surface: '#0D0D11',
		'surface-raised': '#141418',
		'surface-hover': '#1A1A20',
		border: '#24242E',
		'border-soft': '#1C1C24',
		fg: '#E2E2EA',
		'fg-2': '#9898A8',
		'fg-3': '#686878',
		muted: '#444452',
		meta: '#363644',
		accent: '#D97706',
		'accent-on': '#070709',
		success: '#10B981',
		warn: '#F59E0B',
		danger: '#EF4444',
		info: '#3B82F6'
	},
	nick: { saturation: 62, lightness: 64 },
	radius: 2,
	font: 'boson'
};

/** Daylight: Boson's light look. */
export const DAYLIGHT: Builtin = {
	name: 'Daylight',
	colors: {
		bg: '#F4F4F0',
		surface: '#FBFBF9',
		'surface-raised': '#FFFFFF',
		'surface-hover': '#ECECE6',
		border: '#D6D6CE',
		'border-soft': '#E4E4DD',
		fg: '#1B1B21',
		'fg-2': '#4E4E5A',
		'fg-3': '#6E6E7A',
		muted: '#9C9CA6',
		meta: '#C2C2C8',
		accent: '#B45309',
		'accent-on': '#FFFFFF',
		success: '#047857',
		warn: '#B45309',
		danger: '#DC2626',
		info: '#2563EB'
	},
	nick: { saturation: 60, lightness: 38 },
	radius: 2,
	font: 'boson'
};

export function builtin(base: ThemeBase): Builtin {
	return base === 'light' ? DAYLIGHT : TERMINAL;
}

export function emptyTheme(base: ThemeBase = 'dark'): NetworkTheme {
	return { version: 1, base };
}

const hexRe = /^#[0-9a-f]{6}$/i;
export const isHex = (s: unknown): s is string => typeof s === 'string' && hexRe.test(s);

/** Every token's colour: the theme's, else its base's built-in. Lower case. */
export function resolve(t: NetworkTheme): ThemeColors {
	const b = builtin(t.base).colors;
	const out = {} as ThemeColors;
	for (const { key } of THEME_TOKENS) {
		const c = t.colors?.[key];
		out[key] = (isHex(c) ? c : b[key]).toLowerCase();
	}
	return out;
}

/** The theme as stored: empty values and empty panes dropped, numbers clamped. */
export function clean(t: NetworkTheme): NetworkTheme {
	const out: NetworkTheme = { version: 1, base: t.base === 'light' ? 'light' : 'dark' };
	const colors: Partial<ThemeColors> = {};
	for (const { key } of THEME_TOKENS) {
		const c = t.colors?.[key];
		if (isHex(c)) colors[key] = c.toLowerCase();
	}
	if (Object.keys(colors).length) out.colors = colors;
	if (t.nick) {
		const n = (v: number) => Math.max(0, Math.min(100, Math.round(v)));
		out.nick = { saturation: n(t.nick.saturation), lightness: n(t.nick.lightness) };
	}
	if (typeof t.radius === 'number') out.radius = Math.max(0, Math.min(MAX_RADIUS, Math.round(t.radius)));
	if (t.font === 'system' || t.font === 'boson') out.font = t.font;
	const panes: Partial<Record<PaneName, Pane>> = {};
	for (const p of PANES) {
		const src = t.panes?.[p];
		if (!src) continue;
		const pane: Pane = {};
		if (isHex(src.bg)) pane.bg = src.bg.toLowerCase();
		if (typeof src.dim === 'number') pane.dim = Math.round(Math.max(0, Math.min(1, src.dim)) * 100) / 100;
		if (typeof src.blur === 'number') pane.blur = Math.max(0, Math.min(MAX_BLUR, Math.round(src.blur)));
		if (src.position && (POSITIONS as readonly string[]).includes(src.position)) pane.position = src.position;
		if (Object.keys(pane).length) panes[p] = pane;
	}
	if (Object.keys(panes).length) out.panes = panes;
	return out;
}

/** Same document? (after clean) */
export function sameTheme(a: NetworkTheme, b: NetworkTheme): boolean {
	return stable(clean(a)) === stable(clean(b));
}

function stable(v: unknown): string {
	if (Array.isArray(v)) return '[' + v.map(stable).join(',') + ']';
	if (v && typeof v === 'object') {
		const o = v as Record<string, unknown>;
		return (
			'{' +
			Object.keys(o)
				.sort()
				.map((k) => JSON.stringify(k) + ':' + stable(o[k]))
				.join(',') +
			'}'
		);
	}
	return JSON.stringify(v);
}

// ---- contrast (WCAG 2.x) ----

function channel(c: number) {
	const s = c / 255;
	return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}

/** Relative luminance of #rrggbb. */
export function luminance(hex: string): number {
	const n = parseInt(hex.slice(1), 16);
	return 0.2126 * channel((n >> 16) & 255) + 0.7152 * channel((n >> 8) & 255) + 0.0722 * channel(n & 255);
}

/** Contrast ratio of two #rrggbb colours (1–21). */
export function contrast(a: string, b: string): number {
	const la = luminance(a);
	const lb = luminance(b);
	return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

export const MIN_CONTRAST = 4.5;

export interface ContrastIssue {
	fg: string;
	bg: string;
	label: string;
	ratio: number;
	/** The client drops the theme's text colours for this one (fg on bg). */
	guard: boolean;
}

/**
 * Text and background pairs under 4.5:1. Text on the main background is
 * what the client's readability guard checks: below it, Boson keeps the
 * viewer's own text colours.
 */
export function contrastIssues(t: NetworkTheme): ContrastIssue[] {
	const c = resolve(t);
	// [fg token, colour, bg token, colour, what it is, the client's guard]
	const pairs: [string, string, string, string, string, boolean][] = [
		['fg', c.fg, 'bg', c.bg, 'Text on Background', true],
		['fg', c.fg, 'surface', c.surface, 'Text on Panels', false],
		['fg-2', c['fg-2'], 'bg', c.bg, 'Secondary text on Background', false],
		['fg-2', c['fg-2'], 'surface', c.surface, 'Secondary text on Panels', false],
		['accent-on', c['accent-on'], 'accent', c.accent, 'Text on accent buttons', false]
	];
	for (const p of PANES) {
		const bg = t.panes?.[p]?.bg;
		if (isHex(bg))
			pairs.push([
				'fg',
				c.fg,
				`pane-${p}-bg`,
				bg.toLowerCase(),
				`Text on the ${PANE_LABELS[p].toLowerCase()} colour`,
				false
			]);
	}
	const out: ContrastIssue[] = [];
	for (const [fg, fv, bg, bv, label, guard] of pairs) {
		const ratio = contrast(fv, bv);
		if (ratio < MIN_CONTRAST) out.push({ fg, bg, label, ratio, guard });
	}
	return out;
}

// ---- CSS variables for a preview ----

const POS: Record<PanePosition, string> = {
	center: 'center',
	top: 'center top',
	bottom: 'center bottom',
	left: 'left center',
	right: 'right center'
};

/**
 * The CSS custom properties the client sets for a theme: every token, the
 * radius steps, fonts, and per pane --pane-<name>-bg / -image / -dim /
 * -blur / -position. images maps slot to a URL the page may load.
 */
export function themeVars(
	t: NetworkTheme,
	images: Partial<Record<ImageSlot, string>> = {}
): Record<string, string> {
	const c = resolve(t);
	const b = builtin(t.base);
	const vars: Record<string, string> = {};
	for (const { key } of THEME_TOKENS) vars['--' + key] = c[key];
	const r = typeof t.radius === 'number' ? t.radius : b.radius;
	vars['--radius-sm'] = `${r}px`;
	vars['--radius-md'] = `${r * 2}px`;
	vars['--radius-lg'] = `${r * 3}px`;
	const system = '-apple-system, BlinkMacSystemFont, "Segoe UI", Cantarell, Roboto, system-ui, sans-serif';
	const font = t.font ?? b.font;
	vars['--font-body'] = font === 'system' ? system : "'Inter Variable', 'Inter', system-ui, sans-serif";
	vars['--font-display'] =
		font === 'system' ? system : "'JetBrains Mono Variable', 'JetBrains Mono', ui-monospace, monospace";
	const nick = t.nick ?? b.nick;
	vars['--nick-s'] = `${nick.saturation}%`;
	vars['--nick-l'] = `${nick.lightness}%`;
	const paneDefault: Record<PaneName, ThemeToken> = {
		servers: 'bg',
		channels: 'surface',
		messages: 'bg',
		members: 'surface'
	};
	for (const p of PANES) {
		const pane = t.panes?.[p] ?? {};
		const img = images[paneSlot(p)];
		vars[`--pane-${p}-bg`] = isHex(pane.bg) ? pane.bg : c[paneDefault[p]];
		vars[`--pane-${p}-image`] = img ? `url("${img.replace(/["\\\n]/g, '')}")` : 'none';
		vars[`--pane-${p}-dim`] = img ? String(pane.dim ?? 0.6) : '0';
		vars[`--pane-${p}-blur`] = `${pane.blur ?? 0}px`;
		vars[`--pane-${p}-position`] = POS[pane.position ?? 'center'];
	}
	return vars;
}

/** A nick's colour, as the client draws it (hue from the nick). */
export function nickColor(nick: string, t: NetworkTheme): string {
	let h = 0;
	for (const ch of nick) h = (h * 31 + ch.codePointAt(0)!) >>> 0;
	const n = t.nick ?? builtin(t.base).nick;
	return `hsl(${h % 360} ${n.saturation}% ${n.lightness}%)`;
}

/** Svelte action: sets CSS custom properties through the CSSOM (no style attribute). */
export function cssVars(node: HTMLElement, vars: Record<string, string>) {
	let prev: string[] = [];
	const apply = (v: Record<string, string>) => {
		for (const k of prev) if (!(k in v)) node.style.removeProperty(k);
		for (const [k, val] of Object.entries(v)) node.style.setProperty(k, val);
		prev = Object.keys(v);
	};
	apply(vars);
	return { update: apply };
}
