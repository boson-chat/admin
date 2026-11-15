import type { ConfigChange, ConfigView } from './types';

/**
 * Uploads: the files service (files.<domain>). People attach files in the
 * boson app; each upload counts against a rolling quota (deleting doesn't
 * refund it) and is removed after the retention period. Settings are
 * files.* keys of the stack config file; S3 keys stay in .env.
 */

export const DOCS_URL = 'https://github.com/boson-chat/stack/blob/main/docs/files.md';

export const KEYS = {
	enabled: 'files.enabled',
	retention: 'files.retention',
	window: 'files.window',
	account_quota: 'files.account.quota',
	account_max_file: 'files.account.max_file',
	guest_enabled: 'files.guest.enabled',
	guest_quota: 'files.guest.quota',
	guest_max_file: 'files.guest.max_file',
	driver: 'files.storage.driver',
	s3_endpoint: 'files.storage.s3.endpoint',
	s3_bucket: 'files.storage.s3.bucket',
	s3_region: 'files.storage.s3.region',
	s3_path_style: 'files.storage.s3.path_style',
	s3_public_base_url: 'files.storage.s3.public_base_url'
} as const;
export type Field = keyof typeof KEYS;

/** The files service's built-in defaults (the contract). */
export const DEFAULTS = {
	enabled: false,
	retention: '72h',
	window: '72h',
	account_quota: '5GB',
	account_max_file: '100MB',
	guest_enabled: true,
	guest_quota: '500MB',
	guest_max_file: '25MB',
	driver: 'local' as Driver,
	s3_endpoint: '',
	s3_bucket: '',
	s3_region: 'auto',
	s3_path_style: true,
	s3_public_base_url: ''
};

export type Driver = 'local' | 's3';

// ---- the files service's answers ----

export interface FilesLimits {
	quota_bytes: number;
	window_seconds: number;
	max_file_bytes: number;
}
export interface FilesConfigEcho {
	enabled: boolean;
	guest_enabled: boolean;
	limits: { account: FilesLimits; guest: FilesLimits };
	retention_seconds: number;
}
export interface FilesStatus {
	version?: string;
	storage: {
		driver: string;
		ok: boolean;
		error?: string;
		path?: string;
		bucket?: string;
		endpoint?: string;
		free_bytes?: number | null;
		total_bytes?: number | null;
	};
	totals: { files: number; bytes: number; window_uploads: number; window_bytes: number; uploaders: number };
	config?: FilesConfigEcho;
	errors: string[];
}
/** GET /api/files/status. */
export interface FilesView {
	configured: boolean;
	keys_env: string[];
	status?: FilesStatus;
	error?: string;
	error_code?: string;
	checked_at: string;
}
export interface FileRecord {
	id: string;
	url: string;
	name: string;
	size: number;
	content_type: string;
	created_at: string;
	expires_at: string;
	deleted_at?: string | null;
	width?: number;
	height?: number;
	owner: { kind: 'account' | 'guest' | string; id: string; ip_hash?: string };
	username?: string;
}
export interface FilePage {
	files: FileRecord[];
	next_cursor?: string;
}
export interface FilesUsage {
	used: number;
	quota: number;
	window_seconds: number;
	max_file: number;
	frees: { bytes: number; at: string }[];
	files?: number;
}

// ---- sizes (binary: 1 GB = 1024 MB, as the files service reads them) ----

const UNITS: [string, number][] = [
	['TB', 2 ** 40],
	['GB', 2 ** 30],
	['MB', 2 ** 20],
	['KB', 2 ** 10],
	['B', 1]
];
const sizeRe = /^\s*(\d+(?:\.\d+)?)\s*(b|k|kb|kib|m|mb|mib|g|gb|gib|t|tb|tib)?\s*$/i;

/** Bytes of "5GB", "500 MB", "1.5gb"; null when it isn't a size. */
export function parseSize(s: string): number | null {
	const m = sizeRe.exec(s ?? '');
	if (!m) return null;
	const u = (m[2] ?? 'B').toUpperCase().replace(/IB$/, '').replace(/B$/, '');
	const mul = u === '' ? 1 : (UNITS.find(([n]) => n.startsWith(u))?.[1] ?? 1);
	return Math.floor(Number(m[1]) * mul);
}

/** The file's form: the largest whole unit (5GB, 1536MB). */
export function formatSize(n: number): string {
	for (const [name, b] of UNITS) if (n >= b && n % b === 0) return `${n / b}${name}`;
	return `${Math.round(n)}B`;
}

/** For people: 1.2 GB, 512 MB, 900 B. */
export function humanSize(n: number | null | undefined): string {
	if (n === null || n === undefined || Number.isNaN(n)) return '—';
	for (const [name, b] of UNITS) {
		if (n >= b && b > 1) {
			const v = n / b;
			return `${v >= 100 || Number.isInteger(v) ? Math.round(v) : v.toFixed(1)} ${name}`;
		}
	}
	return `${n} B`;
}

export const MIN_FILE = 2 ** 10;
export const MIN_QUOTA = 2 ** 20;
export const MAX_SIZE = 100 * 2 ** 40;

// ---- durations (Go durations in the file: 72h, 90m) ----

const durRe = /^\s*(\d+(?:\.\d+)?)\s*(w|weeks?|d|days?|h|hours?|m|min|minutes?|s|sec|seconds?)\s*$/i;
const durMul: Record<string, number> = { w: 604800, d: 86400, h: 3600, m: 60, s: 1 };

/** Seconds of "72h", "3d", "3 days", "90m", "1w" (or a Go duration like 1h30m). */
export function parseDuration(s: string): number | null {
	const t = (s ?? '').trim();
	const m = durRe.exec(t);
	if (m) {
		const unit = m[2].toLowerCase()[0];
		return Math.round(Number(m[1]) * durMul[unit]);
	}
	// 1h30m, 2h0m0s
	const go = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/.exec(t);
	if (go && (go[1] || go[2] || go[3])) return +(go[1] ?? 0) * 3600 + +(go[2] ?? 0) * 60 + +(go[3] ?? 0);
	return null;
}

/** The file's form (a Go duration): 72h, 90m, 45s. */
export function formatDuration(sec: number): string {
	if (sec % 3600 === 0) return `${sec / 3600}h`;
	if (sec % 60 === 0) return `${sec / 60}m`;
	return `${sec}s`;
}

/** For people: 3 days, 36 hours, 90 minutes. */
export function humanDuration(sec: number | null | undefined): string {
	if (sec === null || sec === undefined || Number.isNaN(sec)) return '—';
	const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;
	if (sec >= 86400 && sec % 86400 === 0) return plural(sec / 86400, 'day');
	if (sec >= 3600 && sec % 3600 === 0) return plural(sec / 3600, 'hour');
	if (sec >= 60 && sec % 60 === 0) return plural(sec / 60, 'minute');
	return plural(sec, 'second');
}

export const MIN_DURATION = 60;
export const MAX_DURATION = 365 * 86400;

// ---- the form ----

export interface Draft {
	enabled: boolean;
	retention: string;
	window: string;
	account_quota: string;
	account_max_file: string;
	guest_enabled: boolean;
	guest_quota: string;
	guest_max_file: string;
	driver: Driver;
	s3_endpoint: string;
	s3_bucket: string;
	s3_region: string;
	s3_path_style: boolean;
	s3_public_base_url: string;
}

/** Where a value on the form comes from. */
export type Source = 'file' | 'service' | 'default';

/**
 * The form as saved: the file's value where it has one, else what the
 * files service runs with (its env default), else the built-in default.
 */
export function draftFrom(view: ConfigView | null, status?: FilesStatus | null) {
	const sources = {} as Record<Field, Source>;
	const c = status?.config;
	const svc: Partial<Draft> = c
		? {
				enabled: c.enabled,
				guest_enabled: c.guest_enabled,
				retention: c.retention_seconds ? formatDuration(c.retention_seconds) : undefined,
				window: c.limits?.account?.window_seconds
					? formatDuration(c.limits.account.window_seconds)
					: undefined,
				account_quota: c.limits?.account?.quota_bytes ? formatSize(c.limits.account.quota_bytes) : undefined,
				account_max_file: c.limits?.account?.max_file_bytes
					? formatSize(c.limits.account.max_file_bytes)
					: undefined,
				guest_quota: c.limits?.guest?.quota_bytes ? formatSize(c.limits.guest.quota_bytes) : undefined,
				guest_max_file: c.limits?.guest?.max_file_bytes
					? formatSize(c.limits.guest.max_file_bytes)
					: undefined,
				driver:
					status?.storage?.driver === 's3' ? 's3' : status?.storage?.driver === 'local' ? 'local' : undefined
			}
		: {};
	const draft = {} as Record<Field, unknown>;
	for (const f of Object.keys(KEYS) as Field[]) {
		const k = view?.keys.find((x) => x.key === KEYS[f]);
		if (k?.set && k.value !== undefined && k.value !== null) {
			sources[f] = 'file';
			draft[f] = k.value;
		} else if (svc[f] !== undefined) {
			sources[f] = 'service';
			draft[f] = svc[f];
		} else {
			sources[f] = 'default';
			draft[f] = DEFAULTS[f];
		}
	}
	return { draft: draft as unknown as Draft, sources };
}

const SIZE_FIELDS = ['account_quota', 'account_max_file', 'guest_quota', 'guest_max_file'] as const;
const DURATION_FIELDS = ['retention', 'window'] as const;
type SizeField = (typeof SIZE_FIELDS)[number];
type DurationField = (typeof DURATION_FIELDS)[number];

export function sizeError(s: string, min: number): string {
	if (!s.trim()) return '';
	const n = parseSize(s);
	if (n === null) return 'A size like 100MB or 5GB.';
	if (n < min || n > MAX_SIZE) return `From ${formatSize(min)} to ${formatSize(MAX_SIZE)}.`;
	return '';
}

export function durationError(s: string): string {
	if (!s.trim()) return '';
	const n = parseDuration(s);
	if (n === null) return 'A time like 72h, 3d or 90m.';
	if (n < MIN_DURATION || n > MAX_DURATION) return 'From 1 minute to 365 days.';
	return '';
}

function urlError(s: string, httpOk: boolean, example: string): string {
	const t = s.trim();
	if (!t) return '';
	try {
		const u = new URL(t);
		if ((u.protocol !== 'https:' && !(httpOk && u.protocol === 'http:')) || u.username || u.search)
			throw new Error();
		return '';
	} catch {
		return `A URL like ${example}.`;
	}
}

export const bucketRe = /^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/;

/** Inline errors by field. */
export function validate(d: Draft): Partial<Record<Field, string>> {
	const out: Partial<Record<Field, string>> = {};
	const set = (f: Field, msg: string) => {
		if (msg && !out[f]) out[f] = msg;
	};
	for (const f of DURATION_FIELDS) set(f, durationError(d[f]));
	set('account_quota', sizeError(d.account_quota, MIN_QUOTA));
	set('guest_quota', sizeError(d.guest_quota, MIN_QUOTA));
	set('account_max_file', sizeError(d.account_max_file, MIN_FILE));
	set('guest_max_file', sizeError(d.guest_max_file, MIN_FILE));
	for (const [q, m] of [
		['account_quota', 'account_max_file'],
		['guest_quota', 'guest_max_file']
	] as const) {
		const qn = parseSize(d[q] || DEFAULTS[q]);
		const mn = parseSize(d[m] || DEFAULTS[m]);
		if (qn !== null && mn !== null && mn > qn) set(m, 'Can’t be bigger than the quota.');
	}
	set('s3_endpoint', urlError(d.s3_endpoint, true, 'https://s3.example.com or http://minio:9000'));
	set('s3_public_base_url', urlError(d.s3_public_base_url, false, 'https://cdn.example.com'));
	if (d.s3_bucket.trim() && (!bucketRe.test(d.s3_bucket.trim()) || d.s3_bucket.includes('..')))
		set('s3_bucket', '3–63 lowercase letters, digits, dots or dashes.');
	if (d.s3_region.trim() && !/^[a-z0-9-]{1,32}$/.test(d.s3_region.trim()))
		set('s3_region', 'Like auto or us-east-1.');
	if (d.driver === 's3') {
		if (!d.s3_endpoint.trim()) set('s3_endpoint', 'Needed for S3 storage.');
		if (!d.s3_bucket.trim()) set('s3_bucket', 'Needed for S3 storage.');
	}
	return out;
}

/** A value in the file's form, for comparing and saving. */
function norm(f: Field, v: Draft[Field]): string | boolean | null {
	if (typeof v === 'boolean') return v;
	const t = (v ?? '').trim();
	if (!t) return null;
	if ((SIZE_FIELDS as readonly string[]).includes(f)) {
		const n = parseSize(t);
		return n === null ? t : formatSize(n);
	}
	if ((DURATION_FIELDS as readonly string[]).includes(f)) {
		const n = parseDuration(t);
		return n === null ? t : formatDuration(n);
	}
	if (f === 's3_endpoint' || f === 's3_public_base_url') return t.replace(/\/+$/, '');
	return t;
}

/** The PATCH /api/config body for what changed. A cleared field is removed from the file. */
export function changes(d: Draft, saved: Draft): ConfigChange {
	const set: Record<string, string | number | boolean> = {};
	const unset: string[] = [];
	for (const f of Object.keys(KEYS) as Field[]) {
		const now = norm(f, d[f]);
		if (now === norm(f, saved[f])) continue;
		if (now === null) unset.push(KEYS[f]);
		else set[KEYS[f]] = now;
	}
	const out: ConfigChange = {};
	if (Object.keys(set).length) out.set = set;
	if (unset.length) out.unset = unset;
	return out;
}

export function changeCount(c: ConfigChange): number {
	return Object.keys(c.set ?? {}).length + (c.unset?.length ?? 0);
}

/** "1.2 GB of 5 GB" and when space comes back. */
export function usageLine(u: FilesUsage, now = Date.now()): string {
	let s = `${humanSize(u.used)} of ${humanSize(u.quota)}`;
	const next = u.frees?.find((f) => new Date(f.at).getTime() > now);
	if (next) s += `, ${humanSize(next.bytes)} frees in ${humanIn(new Date(next.at).getTime() - now)}`;
	return s;
}

/** A short "in" phrase: 20 h, 3 d, 45 min. */
export function humanIn(ms: number): string {
	const m = Math.max(1, Math.round(ms / 60000));
	if (m < 60) return `${m} min`;
	const h = Math.round(m / 60);
	if (h < 48) return `${h} h`;
	return `${Math.round(h / 24)} d`;
}

/** Share of the quota used, 0–1. */
export function usedShare(used: number, quota: number): number {
	if (!quota || quota <= 0) return 0;
	return Math.min(1, Math.max(0, used / quota));
}

export type StorageState = 'ok' | 'failing' | 'unknown';
export function storageState(s?: FilesStatus | null): StorageState {
	if (!s) return 'unknown';
	return s.storage?.ok ? 'ok' : 'failing';
}

export type SizeOrDuration = SizeField | DurationField;
