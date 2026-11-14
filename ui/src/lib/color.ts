/** HSV (h 0–360, s and v 0–1) to #rrggbb. */
export function hsvToHex(h: number, s: number, v: number): string {
	const f = (n: number) => {
		const k = (n + h / 60) % 6;
		return v - v * s * Math.max(0, Math.min(k, 4 - k, 1));
	};
	return (
		'#' +
		[f(5), f(3), f(1)]
			.map((x) =>
				Math.round(x * 255)
					.toString(16)
					.padStart(2, '0')
			)
			.join('')
	);
}

/** #rrggbb to HSV, or null when it isn't a 6-digit hex colour. */
export function hexToHsv(hex: string): { h: number; s: number; v: number } | null {
	const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
	if (!m) return null;
	const n = parseInt(m[1], 16);
	const r = ((n >> 16) & 255) / 255;
	const g = ((n >> 8) & 255) / 255;
	const b = (n & 255) / 255;
	const max = Math.max(r, g, b);
	const d = max - Math.min(r, g, b);
	let h = 0;
	if (d) {
		if (max === r) h = ((g - b) / d) % 6;
		else if (max === g) h = (b - r) / d + 2;
		else h = (r - g) / d + 4;
		h *= 60;
		if (h < 0) h += 360;
	}
	return { h, s: max ? d / max : 0, v: max };
}

/** Normalises typed hex ("D97706", "#abc") to #rrggbb, or null. */
export function normalizeHex(input: string): string | null {
	let t = input.trim().replace(/^#/, '').toLowerCase();
	if (/^[0-9a-f]{3}$/.test(t)) t = [...t].map((c) => c + c).join('');
	return /^[0-9a-f]{6}$/.test(t) ? '#' + t : null;
}

/** Colours offered as swatches: boson's semantic hues plus a spread around the wheel. */
export const presets: { hex: string; name: string }[] = [
	{ hex: '#d97706', name: 'Amber' },
	{ hex: '#eab308', name: 'Yellow' },
	{ hex: '#84cc16', name: 'Lime' },
	{ hex: '#10b981', name: 'Green' },
	{ hex: '#14b8a6', name: 'Teal' },
	{ hex: '#0ea5e9', name: 'Sky' },
	{ hex: '#3b82f6', name: 'Blue' },
	{ hex: '#6366f1', name: 'Indigo' },
	{ hex: '#8b5cf6', name: 'Violet' },
	{ hex: '#d946ef', name: 'Fuchsia' },
	{ hex: '#e11d48', name: 'Rose' },
	{ hex: '#ef4444', name: 'Red' },
	{ hex: '#78716c', name: 'Stone' },
	{ hex: '#64748b', name: 'Slate' }
];
