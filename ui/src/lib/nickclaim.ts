import type { ConfigChange, ConfigView } from './types';

/**
 * Nick registration: the api's nick claims. Boson registers users' nicks
 * with NickServ on other IRC networks, using reg+…@<email domain>
 * addresses whose confirmation mails land in a catch-all POP3 mailbox.
 * Settings are nick_claim.* keys of the stack config file; the password
 * stays in the api's .env.
 */

export const DOCS_URL = 'https://github.com/boson-chat/stack/blob/main/docs/nick-claim.md';

export const KEYS = {
	enabled: 'nick_claim.enabled',
	email_domain: 'nick_claim.email_domain',
	rate_limit_per_hour: 'nick_claim.rate_limit_per_hour',
	host: 'nick_claim.inbound.host',
	port: 'nick_claim.inbound.port',
	tls: 'nick_claim.inbound.tls',
	user: 'nick_claim.inbound.user'
} as const;
export type Field = keyof typeof KEYS;
export const PROTOCOL_KEY = 'nick_claim.inbound.protocol';

export const DEFAULT_PORT = 995;
export const DEFAULT_RATE = 5;
export const MAX_RATE = 10000;

export interface NickClaimWorker {
	running: boolean;
	last_drain_at?: string | null;
	last_ok_at?: string | null;
	last_error?: string;
	consecutive_failures?: number;
}

export interface NickClaimStatus {
	enabled: boolean;
	email_domain: string;
	inbound: { host: string; port: number; tls: boolean; user: string; password_set?: boolean };
	worker: NickClaimWorker;
	pending_claims: number;
	config_error?: string;
}

/** GET /api/nick-claims. */
export interface NickClaimView {
	/** BOSON_API_INTERNAL_URL is set on the panel. */
	configured: boolean;
	password_env: string;
	status?: NickClaimStatus;
	error?: string;
	error_code?: string;
	checked_at: string;
}

export interface NickClaimTestResult {
	ok: boolean;
	message: string;
	messages: number;
}

export interface Draft {
	enabled: boolean;
	email_domain: string;
	rate_limit_per_hour: number | null;
	host: string;
	port: number | null;
	tls: boolean;
	user: string;
}

/** Where a value on the form comes from. */
export type Source = 'file' | 'env' | 'default';

/**
 * The form as saved: the file's value where it has one, else what the api
 * uses (its env default), else the built-in default.
 */
export function draftFrom(view: ConfigView | null, status?: NickClaimStatus | null) {
	const file = (k: string) => view?.keys.find((x) => x.key === k);
	const sources = {} as Record<Field, Source>;
	const pick = <T>(f: Field, api: T | undefined, fallback: T): T => {
		const k = file(KEYS[f]);
		if (k?.set && k.value !== undefined && k.value !== null) {
			sources[f] = 'file';
			return k.value as T;
		}
		if (api !== undefined && api !== null && api !== '') {
			sources[f] = 'env';
			return api;
		}
		sources[f] = 'default';
		return fallback;
	};
	const draft: Draft = {
		enabled: pick('enabled', status?.enabled, false),
		email_domain: pick('email_domain', status?.email_domain, ''),
		// The api doesn't report its rate limit: the file's, else empty (the
		// env default, NICK_CLAIM_RATE_LIMIT_PER_HOUR, 5 unless set).
		rate_limit_per_hour: pick<number | null>('rate_limit_per_hour', undefined, null),
		host: pick('host', status?.inbound.host, ''),
		port: pick<number | null>('port', status?.inbound.port || undefined, DEFAULT_PORT),
		tls: pick('tls', status ? status.inbound.tls : undefined, true),
		user: pick('user', status?.inbound.user, '')
	};
	return { draft, sources };
}

const trim = (s: string) => s.trim();
const hostRe = /^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;
const labelRe = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const ipv4Re = /^(\d{1,3}\.){3}\d{1,3}$/;

export function domainError(s: string): string {
	const d = trim(s).toLowerCase().replace(/\.$/, '');
	if (!d) return '';
	if (ipv4Re.test(d) || d.includes(':') || !hostRe.test(d)) return 'A domain like nick.example.com.';
	return '';
}

export function hostError(s: string): string {
	const h = trim(s).toLowerCase().replace(/\.$/, '');
	if (!h) return '';
	if (ipv4Re.test(h) || /^\[?[0-9a-f:]+\]?$/.test(h) || hostRe.test(h) || labelRe.test(h)) return '';
	return 'A hostname or IP address, like pop3.example.com.';
}

function intError(n: number | null, min: number, max: number, what: string, optional = false): string {
	if (n === null || n === undefined || Number.isNaN(n)) return optional ? '' : `Enter ${what}.`;
	if (!Number.isInteger(n) || n < min || n > max) return `A whole number from ${min} to ${max}.`;
	return '';
}

export const NEEDED_TO_ENABLE = 'Needed to turn nick registration on.';

/** Inline errors by field ('' = fine). */
export function validate(d: Draft): Partial<Record<Field, string>> {
	const out: Partial<Record<Field, string>> = {};
	const set = (f: Field, msg: string) => {
		if (msg) out[f] = msg;
	};
	set('email_domain', domainError(d.email_domain));
	set('host', hostError(d.host));
	set('rate_limit_per_hour', intError(d.rate_limit_per_hour, 1, MAX_RATE, 'a number of claims', true));
	set('port', intError(d.port, 1, 65535, 'a port'));
	if (d.user.length > 254) set('user', 'At most 254 characters.');
	if (d.enabled) {
		if (!trim(d.email_domain)) set('email_domain', NEEDED_TO_ENABLE);
		if (!trim(d.host)) set('host', NEEDED_TO_ENABLE);
	}
	return out;
}

const norm = (f: Field, v: Draft[Field]) => {
	if (typeof v === 'string') return f === 'user' ? trim(v) : trim(v).toLowerCase().replace(/\.$/, '');
	// An emptied number input binds undefined or NaN.
	if (typeof v === 'number' && Number.isNaN(v)) return null;
	return v ?? null;
};

/**
 * The PATCH /api/config body for what changed. A cleared text field is
 * removed from the file (back to the api's env default). The first inbound
 * value written also writes protocol: pop3, as in the docs.
 */
export function changes(d: Draft, saved: Draft, view: ConfigView | null): ConfigChange {
	const set: Record<string, string | number | boolean> = {};
	const unset: string[] = [];
	for (const f of Object.keys(KEYS) as Field[]) {
		const now = norm(f, d[f]);
		if (now === norm(f, saved[f])) continue;
		if (now === '' || now === null) unset.push(KEYS[f]);
		else set[KEYS[f]] = now;
	}
	const inbound = Object.keys(set).some((k) => k.startsWith('nick_claim.inbound.'));
	const proto = view?.keys.find((k) => k.key === PROTOCOL_KEY);
	if (inbound && !proto?.set) set[PROTOCOL_KEY] = 'pop3';
	const out: ConfigChange = {};
	if (Object.keys(set).length) out.set = set;
	if (unset.length) out.unset = unset;
	return out;
}

export function changeCount(c: ConfigChange): number {
	return Object.keys(c.set ?? {}).filter((k) => k !== PROTOCOL_KEY).length + (c.unset?.length ?? 0);
}

/**
 * The test request for the form's mailbox. The api reuses its stored
 * password only for the server it already uses, so another server needs
 * one typed in (for this test only).
 */
export function testBody(d: Draft, password: string) {
	const inbound: Record<string, string | number | boolean> = { protocol: 'pop3', tls: d.tls };
	if (trim(d.host)) inbound.host = trim(d.host).toLowerCase().replace(/\.$/, '');
	if (typeof d.port === 'number' && Number.isInteger(d.port)) inbound.port = d.port;
	if (trim(d.user)) inbound.user = trim(d.user);
	if (password) inbound.password = password;
	return { inbound };
}

/** The form's server isn't the one the api uses (its password won't be reused). */
export function otherServer(d: Draft, status?: NickClaimStatus | null): boolean {
	const now = trim(d.host).toLowerCase().replace(/\.$/, '');
	return !!now && !!status && now !== (status.inbound.host ?? '').toLowerCase();
}

export type PasswordState = 'set' | 'missing' | 'unknown';
export function passwordState(status?: NickClaimStatus | null): PasswordState {
	const p = status?.inbound.password_set;
	return p === true ? 'set' : p === false ? 'missing' : 'unknown';
}

export type WorkerState = 'off' | 'running' | 'failing' | 'stopped';
/** One word for the worker: off (feature off), running, failing (errors) or stopped. */
export function workerState(status?: NickClaimStatus | null): WorkerState {
	if (!status) return 'stopped';
	const w = status.worker;
	if (w.running && ((w.consecutive_failures ?? 0) > 0 || !!w.last_error)) return 'failing';
	if (w.running) return 'running';
	if (!status.enabled) return 'off';
	return (w.consecutive_failures ?? 0) > 0 || w.last_error || status.config_error ? 'failing' : 'stopped';
}

/** The pattern of the addresses handed to NickServ. */
export function exampleAddress(domain: string): string {
	const d = trim(domain).toLowerCase() || 'example.com';
	return `reg+<user id>-<token>@${d}`;
}
