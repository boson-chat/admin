import type { ChartSeries, ChartUnit, HostStats, MetricsRange, ScrapeTarget } from './types';

/** The ranges the page offers (the BFF accepts exactly these). */
export const ranges: { value: MetricsRange; label: string }[] = [
	{ value: '1h', label: '1h' },
	{ value: '6h', label: '6h' },
	{ value: '24h', label: '24h' },
	{ value: '7d', label: '7d' }
];

export function isRange(v: string | null): v is MetricsRange {
	return ranges.some((r) => r.value === v);
}

/** How often the page refreshes while visible. */
export const refreshMs = 30_000;

// ---- formatting ----

const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

/** 1536 → "1.5 KB" (powers of 1024, as free and df -h show memory and disks). */
export function bytes(v: number | null | undefined, digits = 1): string {
	if (v === null || v === undefined || !Number.isFinite(v)) return '—';
	let i = 0;
	let n = Math.abs(v);
	while (n >= 1000 && i < units.length - 1) {
		n /= 1024;
		i++;
	}
	const s = i === 0 ? String(Math.round(n)) : n.toFixed(n >= 100 ? 0 : digits);
	return (v < 0 ? '-' : '') + s + ' ' + units[i];
}

export function rate(v: number | null | undefined): string {
	return v === null || v === undefined ? '—' : bytes(v) + '/s';
}

/** 37.123 → "37%", 4.25 → "4.3%", 0.04 → "<0.1%". */
export function pct(v: number | null | undefined): string {
	if (v === null || v === undefined || !Number.isFinite(v)) return '—';
	if (v > 0 && v < 0.1) return '<0.1%';
	return (v >= 10 ? Math.round(v).toString() : (Math.round(v * 10) / 10).toString()) + '%';
}

/** 2.456 → "2.46", 12.3 → "12.3". */
export function loadNum(v: number | null | undefined): string {
	if (v === null || v === undefined || !Number.isFinite(v)) return '—';
	return v >= 10 ? v.toFixed(1) : v.toFixed(2);
}

/** Seconds → "3d 4h", "5h 12m", "42m". */
export function duration(secs: number | null | undefined): string {
	if (secs === null || secs === undefined || !Number.isFinite(secs) || secs < 0) return '—';
	const d = Math.floor(secs / 86400);
	const h = Math.floor((secs % 86400) / 3600);
	const m = Math.floor((secs % 3600) / 60);
	if (d) return h ? `${d}d ${h}h` : `${d}d`;
	if (h) return m ? `${h}h ${m}m` : `${h}h`;
	return `${Math.max(m, secs >= 60 ? 1 : 0)}m`;
}

/** A value in a chart's unit. */
export function formatUnit(unit: ChartUnit, v: number | null | undefined): string {
	if (unit === 'percent') return pct(v);
	if (unit === 'bytes_per_sec') return rate(v);
	return loadNum(v);
}

/** Axis tick label: shorter than formatUnit. */
export function tickLabel(unit: ChartUnit, v: number): string {
	if (unit === 'percent') return `${Math.round(v)}%`;
	if (unit === 'bytes_per_sec') {
		if (v === 0) return '0';
		const b = bytes(v, 1).replace(' ', '');
		return b.replace(/\.0(?=[A-Z])/, '');
	}
	return String(+v.toFixed(2));
}

// ---- derived numbers and thresholds ----

export type Level = 'ok' | 'warn' | 'bad';

export interface Derived {
	memUsed: number | null;
	memUsedPct: number | null;
	swapUsed: number | null;
	/** null: no swap configured (or no data). */
	swapUsedPct: number | null;
	loadPerCore: number | null;
}

function ratio(a: number | null, b: number | null): number | null {
	return a === null || b === null || b <= 0 ? null : a / b;
}

export function derive(s: HostStats): Derived {
	const memUsed =
		s.mem_total_bytes !== null && s.mem_available_bytes !== null
			? s.mem_total_bytes - s.mem_available_bytes
			: null;
	const swapUsed =
		s.swap_total_bytes !== null && s.swap_free_bytes !== null ? s.swap_total_bytes - s.swap_free_bytes : null;
	const mp = ratio(memUsed, s.mem_total_bytes);
	const sp = ratio(swapUsed, s.swap_total_bytes);
	return {
		memUsed,
		memUsedPct: mp === null ? null : mp * 100,
		swapUsed,
		swapUsedPct: sp === null ? null : sp * 100,
		loadPerCore: ratio(s.load1, s.cores)
	};
}

/**
 * Thresholds. The stack's box is small (2 vCPU / 2 GB), so memory and
 * swap matter most: any swap in use is worth a look.
 */
export const thresholds = {
	cpu: { warn: 80, bad: 95 },
	memory: { warn: 80, bad: 90 },
	/** Percent of swap used; anything above 0 is already "warn". */
	swap: { warn: 0, bad: 50 },
	disk: { warn: 85, bad: 95 },
	/** load1 / cores */
	load: { warn: 1, bad: 2 },
	pressure: { warn: 10, bad: 25 }
} as const;

export type ThresholdKey = keyof typeof thresholds;

export function level(key: ThresholdKey, v: number | null | undefined): Level {
	if (v === null || v === undefined || !Number.isFinite(v)) return 'ok';
	const t = thresholds[key];
	if (v >= t.bad) return 'bad';
	// Swap: a rounding crumb of a few KB isn't "in use".
	if (key === 'swap') return v >= 0.5 ? 'warn' : 'ok';
	return v >= t.warn ? 'warn' : 'ok';
}

/** The word a tile shows beside a non-ok level (never colour alone). */
export function levelWord(key: ThresholdKey, l: Level): string {
	if (l === 'ok') return '';
	if (key === 'swap') return l === 'bad' ? 'Heavy swapping' : 'In use';
	if (key === 'disk') return l === 'bad' ? 'Nearly full' : 'Filling up';
	if (key === 'load') return l === 'bad' ? 'Overloaded' : 'Busy';
	return l === 'bad' ? 'Critical' : 'High';
}

export function usedPct(size: number, avail: number): number {
	return size > 0 ? (1 - avail / size) * 100 : 0;
}

// ---- service health ----

export function targetLevel(t: ScrapeTarget): Level {
	return t.status === 'up' ? 'ok' : t.status === 'stale' ? 'warn' : 'bad';
}

export function targetSummary(ts: ScrapeTarget[]): string {
	if (!ts.length) return 'No scrape targets reported.';
	const down = ts.filter((t) => t.status !== 'up').map((t) => t.job);
	if (!down.length) return ts.length === 1 ? 'The one target is up.' : `All ${ts.length} targets are up.`;
	const uniq = [...new Set(down)];
	return `${uniq.join(', ')} ${uniq.length === 1 ? 'is' : 'are'} not answering.`;
}

// ---- chart geometry ----

export function yTicks(max: number, count = 4): number[] {
	return Array.from({ length: count + 1 }, (_, i) => (max * i) / count);
}

/** 1, 2, 2.5, 5 × 10^n step ≥ v. */
function niceStep(v: number): number {
	const p = Math.pow(10, Math.floor(Math.log10(v)));
	for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v * 0.999) return m * p;
	return 10 * p;
}

/**
 * The y-axis for data up to m: round ticks (at most `count` + 1 of them),
 * in powers of 1024 for byte rates so labels read "200KB", "1MB".
 * fixed (100 for percentages) wins.
 */
export function axis(
	m: number,
	unit: ChartUnit,
	fixed?: number,
	count = 4
): { max: number; ticks: number[] } {
	if (fixed) return { max: fixed, ticks: yTicks(fixed, count) };
	let base = 1;
	if (unit === 'bytes_per_sec') {
		if (!(m > 0)) m = 1024;
		while (m / base >= 1024) base *= 1024;
	} else if (!(m > 0)) m = 1;
	const v = m / base;
	const step = niceStep(v / count);
	const n = Math.max(1, Math.ceil(v / step - 1e-9));
	return { max: n * step * base, ticks: Array.from({ length: n + 1 }, (_, i) => i * step * base) };
}

/** Time ticks for a span: round times, about `want` of them. */
export function xTicks(start: number, end: number, want: number): number[] {
	const span = end - start;
	if (span <= 0 || want < 1) return [];
	const steps = [300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400, 172800];
	const step = steps.find((s) => span / s <= want) ?? 172800;
	// Align to local midnight-based boundaries.
	const tz = new Date(start * 1000).getTimezoneOffset() * 60;
	const first = Math.ceil((start - tz) / step) * step + tz;
	const out: number[] = [];
	for (let t = first; t <= end; t += step) out.push(t);
	return out;
}

const hm = new Intl.DateTimeFormat('en', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
const dayFmt = new Intl.DateTimeFormat('en', { weekday: 'short', day: 'numeric' });
const full = new Intl.DateTimeFormat('en', {
	weekday: 'short',
	hour: '2-digit',
	minute: '2-digit',
	hourCycle: 'h23'
});

export function xTickLabel(t: number, span: number): string {
	const d = new Date(t * 1000);
	if (span > 2 * 86400) return d.getHours() === 0 && d.getMinutes() === 0 ? dayFmt.format(d) : hm.format(d);
	return hm.format(d);
}

export function tooltipTime(t: number): string {
	return full.format(new Date(t * 1000));
}

/** Splits a series where points are missing (more than 2.5 steps apart). */
export function segments(points: [number, number][], step: number): [number, number][][] {
	const out: [number, number][][] = [];
	let cur: [number, number][] = [];
	for (const p of points) {
		if (cur.length && p[0] - cur[cur.length - 1][0] > step * 2.5) {
			out.push(cur);
			cur = [];
		}
		cur.push(p);
	}
	if (cur.length) out.push(cur);
	return out;
}

/** Every timestamp any series has, sorted. */
export function timestamps(series: ChartSeries[]): number[] {
	const s = new Set<number>();
	for (const x of series) for (const p of x.points) s.add(p[0]);
	return [...s].sort((a, b) => a - b);
}

/**
 * Stacks series on shared timestamps: for each series, [t, bottom, top].
 * A series missing a timestamp contributes 0 there.
 */
export function stack(series: ChartSeries[]): [number, number, number][][] {
	const ts = timestamps(series);
	const base = new Map(ts.map((t) => [t, 0]));
	return series.map((s) => {
		const at = new Map(s.points);
		return ts.map((t) => {
			const b = base.get(t)!;
			const top = b + (at.get(t) ?? 0);
			base.set(t, top);
			return [t, b, top] as [number, number, number];
		});
	});
}

/** The largest value drawn (stack tops when stacked). */
export function dataMax(series: ChartSeries[], stacked: boolean): number {
	if (stacked) {
		const st = stack(series);
		return st.length ? Math.max(0, ...st[st.length - 1].map((p) => p[2])) : 0;
	}
	let m = 0;
	for (const s of series) for (const p of s.points) m = Math.max(m, p[1]);
	return m;
}

/** Index of the timestamp nearest t (ts sorted). */
export function nearest(ts: number[], t: number): number {
	if (!ts.length) return -1;
	let lo = 0;
	let hi = ts.length - 1;
	while (hi - lo > 1) {
		const mid = (lo + hi) >> 1;
		if (ts[mid] <= t) lo = mid;
		else hi = mid;
	}
	return Math.abs(ts[lo] - t) <= Math.abs(ts[hi] - t) ? lo : hi;
}

/** The value of a series at t, if it has a point there. */
export function valueAt(s: ChartSeries, t: number): number | null {
	const p = s.points.find((x) => x[0] === t);
	return p ? p[1] : null;
}

/** The newest value of a series. */
export function latest(s: ChartSeries): number | null {
	return s.points.length ? s.points[s.points.length - 1][1] : null;
}

/** Up to n evenly spaced rows for the table view (always including the newest). */
export function sample<T>(rows: T[], n: number): T[] {
	if (rows.length <= n) return rows;
	const out: T[] = [];
	for (let i = 0; i < n; i++) out.push(rows[Math.round(((rows.length - 1) * i) / (n - 1))]);
	return out;
}
