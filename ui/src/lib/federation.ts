/**
 * The Federation page: this server's rules for people who sign in with an
 * account from their home Boson server (alice@boson.chat), the remote users
 * who did, and this server as a home. The BFF saves the settings to the
 * stack config file's auth.federation section (or auth's database), and
 * auth enforces them (auth docs/federation.md).
 */
import type { Choice } from './components/ChoiceGroup.svelte';

export type FederationMode = 'off' | 'allowlist' | 'directory' | 'open';
export type Source = 'file' | 'database' | 'env' | 'default';
export type Store = 'file' | 'auth' | 'env';

export interface FederationSettings {
	mode: FederationMode;
	allow: string[];
	block: string[];
	directory_url: string;
	/** "" = remote users get the account quota. */
	remote_upload_quota: string;
	remote_voice: boolean;
}

export type Field = keyof FederationSettings;

export interface HomeCheck {
	id: string;
	label: string;
	ok: boolean;
	warn?: boolean;
	detail?: string;
}

export type HomeState = 'published' | 'missing' | 'off' | 'mismatch' | 'unreachable' | 'unknown';

export interface Home {
	domain?: string;
	url?: string;
	public_url?: string;
	state: HomeState;
	federation?: {
		version: number;
		issuer: string;
		jwks_uri: string;
		assertion_endpoint: string;
		login_endpoint: string;
		accepts: string;
	};
	checks: HomeCheck[];
	error?: string;
	checked_at: string;
}

export interface FederationView {
	supported: boolean;
	requires?: string;
	error?: string;
	settings: FederationSettings;
	sources: Record<Field, Source>;
	store: Store;
	editable: boolean;
	fields: { name: Field; key: string; env: string }[];
	defaults: FederationSettings;
	issuer?: string;
	domain?: string;
	remote_users: number;
	/** The role new remote users get ("" = none). */
	remote_role?: string;
	/** auth's last stored change (database store). */
	updated_at?: string;
	updated_by?: string;
	/** auth reads the stack config file but this panel doesn't have it. */
	auth_uses_file?: boolean;
	/** The running auth release, when the panel can read it. */
	auth_version?: string;
	/** auth < 1.26.0: a domain block doesn't drop users already on IRC. */
	block_keeps_irc?: boolean;
	home?: Home;
}

export interface RemoteUser {
	id: string;
	identity: string;
	handle: string;
	home_domain: string;
	home_username?: string;
	display_name?: string;
	picture?: string | null;
	first_seen_at: string;
	last_login_at?: string | null;
	last_seen_at?: string | null;
	/** The local account is disabled (a ban does that). */
	disabled?: boolean;
	banned: boolean;
	banned_at?: string | null;
	ban_reason?: string;
	domain_blocked?: boolean;
	sessions: number;
	/** The panel saved a block change auth hasn't applied yet. */
	applying?: boolean;
}

export interface RemoteUserPage {
	users: RemoteUser[];
	next_cursor?: string;
	total: number;
}

export interface BlockResult {
	domain: string;
	revoked: number;
	revoke_error?: string;
	view: FederationView;
}

export const DOCS_URL = 'https://github.com/boson-chat/auth/blob/main/docs/federation.md';
export const DEFAULT_DIRECTORY = 'https://directory.boson.chat';

/** The trust modes, closed to open, each with what it costs. */
export const MODES: (Choice<FederationMode> & { risk: string })[] = [
	{
		value: 'off',
		label: 'Off',
		detail:
			'Only accounts made here can sign in. Your own users can’t use their account on other Boson servers either.',
		risk: 'Safest. Nobody from outside gets in, and your users lose “Continue as…” elsewhere.'
	},
	{
		value: 'allowlist',
		label: 'Allowlist',
		detail: 'Only people from the home servers you list below can sign in.',
		risk: 'Tight. You vouch for each server yourself, and have to add new ones by hand.'
	},
	{
		value: 'directory',
		label: 'Verified servers',
		detail:
			'People from any server with a verified listing in the directory can sign in. Verified means it proved control of its domain with DNS.',
		risk: 'Balanced. A verified listing proves who runs a domain, not that they moderate it well. Block the ones that cause trouble.',
		note: 'Default'
	},
	{
		value: 'open',
		label: 'Open',
		detail: 'People from any Boson server whose discovery and signatures check out can sign in.',
		risk: 'Widest. Anyone can set up a server in minutes and sign in from it, so spam and ban evasion are easier. Watch the remote users list.'
	}
];

export const FIELD_LABELS: Record<Field, string> = {
	mode: 'Who can sign in',
	allow: 'Allowlist',
	block: 'Blocklist',
	directory_url: 'Directory',
	remote_upload_quota: 'Remote users’ upload quota',
	remote_voice: 'Remote users in voice'
};

const domainRe = /^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;

/**
 * Cleans what someone typed into a home domain, or *.domain for it and its
 * subdomains ("" when it isn't one).
 */
export function cleanDomain(s: string): string {
	let d = s.trim().toLowerCase();
	const wild = d.startsWith('*.');
	if (wild) d = d.slice(2);
	d = d.replace(/^https?:\/\//, '');
	d = d.replace(/\/.*$/, '');
	d = d.replace(/^[^@]*@/, ''); // alice@boson.chat → boson.chat
	d = d.replace(/\.$/, '');
	if (!domainRe.test(d)) return '';
	return wild ? '*.' + d : d;
}

/** Does a list entry (domain or *.domain) cover a home domain? */
export function domainMatches(entry: string, domain: string): boolean {
	if (entry.startsWith('*.')) {
		const base = entry.slice(2);
		return domain === base || domain.endsWith('.' + base);
	}
	return entry === domain;
}

/** Why a typed domain can't be added, or ''. */
export function domainProblem(s: string, list: string[], other?: { name: string; list: string[] }): string {
	if (!s.trim()) return '';
	const d = cleanDomain(s);
	if (!d)
		return `“${s.trim()}” isn’t a domain. Type the home server’s domain, like boson.chat, or *.example.org for it and its subdomains.`;
	if (list.includes(d)) return `${d} is already listed.`;
	if (other?.list.includes(d)) return `${d} is on the ${other.name}. Take it off there first.`;
	return '';
}

const sizeRe = /^\s*(\d+(\.\d+)?)\s*(b|k|kb|m|mb|g|gb|t|tb|kib|mib|gib|tib)?\s*$/i;

/** Why a quota can't be saved, or '' (empty = the account quota). */
export function quotaProblem(s: string): string {
	if (!s.trim()) return '';
	return sizeRe.test(s) ? '' : 'Use a size like 500MB or 2GB, or leave it empty for the account quota.';
}

export function directoryProblem(s: string): string {
	const v = s.trim();
	if (!v) return 'Enter the directory’s URL, or reset it to the default.';
	try {
		const u = new URL(v);
		if (u.protocol !== 'https:' || !u.host || u.username) return 'The directory must be an https:// URL.';
	} catch {
		return 'The directory must be an https:// URL, like https://directory.boson.chat.';
	}
	return '';
}

export type Draft = FederationSettings;

export function draftFrom(v: FederationView): Draft {
	const s = v.settings;
	return {
		mode: s.mode,
		allow: [...(s.allow ?? [])],
		block: [...(s.block ?? [])],
		directory_url: s.directory_url ?? '',
		remote_upload_quota: s.remote_upload_quota ?? '',
		remote_voice: !!s.remote_voice
	};
}

/** Field problems in a draft (the BFF checks the same). */
export function validate(d: Draft): Partial<Record<Field, string>> {
	const out: Partial<Record<Field, string>> = {};
	if (d.mode === 'allowlist' && d.allow.length === 0)
		out.allow = 'Add at least one home server, or pick another mode: an empty allowlist lets nobody in.';
	const both = d.allow.find((x) => d.block.includes(x));
	if (both) out.block = `${both} is on both lists. Take it off one.`;
	if (d.mode === 'directory') {
		const p = directoryProblem(d.directory_url);
		if (p) out.directory_url = p;
	}
	const q = quotaProblem(d.remote_upload_quota);
	if (q) out.remote_upload_quota = q;
	return out;
}

const same = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);

/** The PATCH body for what changed. */
export function changes(d: Draft, saved: Draft): Partial<FederationSettings> {
	const out: Partial<FederationSettings> = {};
	if (d.mode !== saved.mode) out.mode = d.mode;
	if (!same(d.allow, saved.allow)) out.allow = [...d.allow];
	if (!same(d.block, saved.block)) out.block = [...d.block];
	if (d.directory_url.trim() !== saved.directory_url) out.directory_url = d.directory_url.trim();
	if (d.remote_upload_quota.trim() !== saved.remote_upload_quota)
		out.remote_upload_quota = d.remote_upload_quota.trim();
	if (d.remote_voice !== saved.remote_voice) out.remote_voice = d.remote_voice;
	return out;
}

/** Where a value comes from, in words. */
export function sourceText(src: Source | undefined, env: string): string {
	// Every field has a FEDERATION_* env var in auth.
	switch (src) {
		case 'file':
			return 'Set in config/boson.yaml';
		case 'database':
			return 'Set in the panel (auth’s database)';
		case 'env':
			return `From auth’s ${env}`;
		default:
			return 'Built-in default';
	}
}

/** How the home status reads at a glance. */
export function homeSummary(h: Home | undefined): {
	tone: 'ok' | 'warn' | 'bad' | 'off';
	title: string;
	text: string;
} {
	switch (h?.state) {
		case 'published':
			return h.checks.some((c) => !c.ok)
				? {
						tone: 'warn',
						title: 'Published, with a warning',
						text: 'Your users can sign in on other servers. One detail is out of date.'
					}
				: {
						tone: 'ok',
						title: 'Published',
						text: 'Your users can sign in on other Boson servers as user@' + (h.domain ?? 'your domain') + '.'
					};
		case 'off':
			return {
				tone: 'off',
				title: 'Federation is off',
				text: 'boson.json says accepts: off, so your users can’t sign in elsewhere and nobody can sign in here from outside.'
			};
		case 'missing':
			return {
				tone: 'bad',
				title: 'Not published',
				text: 'boson.json has no federation block, so other servers can’t verify your users.'
			};
		case 'mismatch':
			return {
				tone: 'bad',
				title: 'Published, but broken',
				text: 'Other servers will refuse your users until the problems below are fixed.'
			};
		case 'unreachable':
			return {
				tone: 'bad',
				title: 'Can’t read boson.json',
				text: 'The panel couldn’t fetch it. Other servers may not be able to either.'
			};
		default:
			return {
				tone: 'off',
				title: 'Unknown',
				text: h?.error ?? 'The panel doesn’t know this server’s domain.'
			};
	}
}

/** "alice@boson.chat" → ["alice", "boson.chat"]. */
export function splitIdentity(id: string): [string, string] {
	const i = id.lastIndexOf('@');
	return i < 0 ? [id, ''] : [id.slice(0, i), id.slice(i + 1)];
}

/**
 * What blocking a home server does, in words. From auth 1.26.0 a block
 * also drops the server's users from IRC within seconds; before it, people
 * already connected stayed until they disconnected. keepsIrc is that older
 * behaviour (only when the panel knows auth is older).
 */
export function blockWords(keepsIrc: boolean) {
	return keepsIrc
		? {
				action: 'Block and end sessions',
				saveAction: 'Save and end their sessions',
				effect:
					'Their sessions here end now. Anyone already connected to IRC stays connected until they disconnect; they can’t reconnect. To drop someone at once, ban them.',
				help: 'Refused in every mode. Saving a new block also ends that server’s sessions here (people already on IRC stay until they disconnect).'
			}
		: {
				action: 'Block and disconnect',
				saveAction: 'Save and disconnect them',
				effect:
					'Everyone from there is signed out now and dropped from IRC within seconds, and can’t reconnect while it’s blocked.',
				help: 'Refused in every mode. Blocking signs a server’s users out and drops them from IRC within seconds; unblocking lets them sign in again.'
			};
}

/** Marks the rows a block change covers until auth confirms it. */
export function markBlocked(users: RemoteUser[], entry: string, blocked: boolean): RemoteUser[] {
	return users.map((u) =>
		domainMatches(entry, u.home_domain) && !!u.domain_blocked !== blocked
			? { ...u, domain_blocked: blocked, applying: true }
			: u
	);
}
