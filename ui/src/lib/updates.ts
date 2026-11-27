// The Updates page: types of GET /api/updates and pure helpers.

export interface Impact {
	restart: boolean;
	disruptive: boolean;
	chat_disconnect: boolean;
	drops_calls: boolean;
	downtime_seconds: number;
	who: string;
}

export interface Release {
	tag: string;
	version: string;
	name?: string;
	body: string;
	url?: string;
	published_at: string;
	breaking?: string[];
	published: boolean;
}

export interface ComponentStatus {
	id: string;
	name: string;
	about: string;
	repo: string;
	mode: 'image' | 'stack';
	installed?: string;
	present: boolean;
	add_on?: boolean;
	latest?: string;
	update: boolean;
	releases: Release[];
	pending?: string[];
	breaking: boolean;
	breaking_notes?: string[];
	impact: Impact;
	services: string[];
	never_restart?: string[];
	applyable: boolean;
	why?: string;
	error?: string;
}

export interface Step {
	id: string;
	label: string;
	state: 'pending' | 'running' | 'ok' | 'failed' | 'skipped';
	detail?: string;
	started_at?: string;
	finished_at?: string;
}

export interface Check {
	id: string;
	label: string;
	ok: boolean;
	skipped?: boolean;
	detail?: string;
}

export interface Item {
	component: string;
	name: string;
	from: string;
	to: string;
	from_image: string;
	to_image: string;
	services: string[];
	self?: boolean;
	pinned_image?: string;
}

export interface Backup {
	dir: string;
	bytes: number;
	files: { name: string; bytes: number }[];
	pruned?: string[];
	free_bytes?: number;
}

export type RunState = 'running' | 'restarting' | 'succeeded' | 'failed' | 'rolled_back' | 'rollback_failed';

export interface Run {
	id: string;
	kind: 'update' | 'rollback';
	actor: string;
	state: RunState;
	started_at: string;
	finished_at?: string;
	items: Item[];
	skipped?: string[];
	summary: string;
	steps: Step[];
	checks: Check[];
	backup?: Backup;
	migrations_ran?: string[];
	restore_data?: boolean;
	error?: string;
	changed: boolean;
	can_rollback: boolean;
	rollback_of?: string;
	rolled_back_by?: string;
	log: string[];
}

export interface RunSummary {
	id: string;
	kind: 'update' | 'rollback';
	actor: string;
	state: RunState;
	started_at: string;
	finished_at?: string;
	items: Item[];
	error?: string;
	backup_dir?: string;
}

export interface UpdatesStatus {
	checked_at?: string;
	next_check_at?: string;
	checking: boolean;
	available: number;
	components: ComponentStatus[];
	rate_limit?: { limit: number; remaining: number; reset: string; authenticated: boolean };
	error?: string;
	apply: { enabled: boolean; why?: string; self_update: boolean; backup_root?: string; keep: number };
	run?: Run;
	history: RunSummary[];
}

export interface Preview {
	items: Item[];
	skipped: string[];
	summary: string;
	breaking: boolean;
	impacts: { component: string; name: string; impact: Impact }[];
}

/** Stack-page service names (overview) for each component. */
export const overviewName: Record<string, string> = {
	voice: 'quicvoiced',
	video: 'quicvideod',
	ergo: 'irc'
};

/** Latest version by overview service name, for components with an update. */
export function updatesByService(st: UpdatesStatus | null | undefined): Record<string, string> {
	const out: Record<string, string> = {};
	for (const c of st?.components ?? []) if (c.update && c.latest) out[overviewName[c.id] ?? c.id] = c.latest;
	return out;
}

export function running(r: Run | undefined | null): boolean {
	return !!r && (r.state === 'running' || r.state === 'restarting');
}

export const stateWord: Record<RunState, string> = {
	running: 'Running',
	restarting: 'Panel restarting',
	succeeded: 'Done',
	failed: 'Failed',
	rolled_back: 'Rolled back',
	rollback_failed: 'Rollback failed'
};

export const stateTag: Record<RunState, string> = {
	running: 'tag-accent',
	restarting: 'tag-accent',
	succeeded: 'tag-ok',
	failed: 'tag-bad',
	rolled_back: 'tag-warn',
	rollback_failed: 'tag-bad'
};

/** "affects everyone" / "brief pause" / "nobody notices", for a tag. */
export function impactWord(i: Impact): { text: string; tone: string } {
	if (i.chat_disconnect) return { text: 'disconnects chat', tone: 'tag-bad' };
	if (i.drops_calls) return { text: 'drops calls', tone: 'tag-bad' };
	if (i.disruptive) return { text: 'interrupts users', tone: 'tag-warn' };
	if (i.restart) return { text: 'brief pause', tone: '' };
	return { text: 'no restart', tone: 'tag-ok' };
}

export function bytes(n: number): string {
	if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + ' GiB';
	if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MiB';
	if (n >= 1 << 10) return Math.round(n / 1024) + ' KiB';
	return n + ' B';
}

const esc = (s: string) =>
	s
		.replace(/&/g, '&amp;')
		.replace(/</g, '&lt;')
		.replace(/>/g, '&gt;')
		.replace(/"/g, '&quot;')
		.replace(/'/g, '&#39;');

/** Only http(s) links survive; everything else is dropped to its text. */
function safeURL(u: string): string | null {
	try {
		const p = new URL(u);
		return p.protocol === 'https:' || p.protocol === 'http:' ? p.href : null;
	} catch {
		return null;
	}
}

/** One line of Markdown (code, bold, links), escaped first like renderMarkdown. */
export function renderInline(s: string): string {
	return inline(s);
}

function inline(s: string): string {
	// Escape first: nothing in a release body can become markup.
	let out = esc(s);
	const codes: string[] = [];
	out = out.replace(/`([^`]+)`/g, (_, c: string) => {
		codes.push(c);
		return `\u2063code${codes.length - 1}\u2063`;
	});
	out = out.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (m, text: string, href: string) => {
		const u = safeURL(href.replace(/&amp;/g, '&'));
		return u ? `<a href="${esc(u)}" target="_blank" rel="noopener noreferrer">${text}</a>` : text;
	});
	out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
	out = out.replace(/(^|[\s(])\*([^*\s][^*]*)\*/g, '$1<em>$2</em>');
	out = out.replace(/\u2063code(\d+)\u2063/g, (_, i: string) => `<code>${codes[Number(i)]}</code>`);
	return out;
}

/**
 * A small, safe Markdown renderer for semantic-release changelogs:
 * headings, lists, paragraphs, code (inline and fenced), bold, italic and
 * http(s) links. All input text is HTML-escaped before any markup is added,
 * so raw HTML in a release body shows as text and never runs.
 */
export function renderMarkdown(md: string): string {
	const lines = (md ?? '').replace(/\r\n?/g, '\n').split('\n');
	const out: string[] = [];
	let list: 'ul' | 'ol' | null = null;
	let para: string[] = [];
	let fence: string[] | null = null;
	const flushPara = () => {
		if (para.length) out.push(`<p>${inline(para.join(' '))}</p>`);
		para = [];
	};
	const closeList = () => {
		if (list) out.push(`</${list}>`);
		list = null;
	};
	for (const raw of lines) {
		if (fence) {
			if (/^\s*```/.test(raw)) {
				out.push(`<pre><code>${esc(fence.join('\n'))}</code></pre>`);
				fence = null;
			} else fence.push(raw);
			continue;
		}
		const line = raw.trimEnd();
		if (/^\s*```/.test(line)) {
			flushPara();
			closeList();
			fence = [];
			continue;
		}
		const h = /^(#{1,6})\s+(.*)$/.exec(line);
		if (h) {
			flushPara();
			closeList();
			// Release bodies start at ##; keep them below the page's own headings.
			const level = Math.min(6, h[1].length + 2);
			out.push(`<h${level}>${inline(h[2])}</h${level}>`);
			continue;
		}
		const li = /^\s*([*+-]|\d+[.)])\s+(.*)$/.exec(line);
		if (li) {
			flushPara();
			const kind = /\d/.test(li[1]) ? 'ol' : 'ul';
			if (list !== kind) {
				closeList();
				out.push(`<${kind}>`);
				list = kind;
			}
			out.push(`<li>${inline(li[2])}</li>`);
			continue;
		}
		if (!line.trim()) {
			flushPara();
			closeList();
			continue;
		}
		if (list && /^\s{2,}/.test(raw)) {
			// A continuation of the last list item.
			out[out.length - 1] = out[out.length - 1].replace(/<\/li>$/, ' ' + inline(line.trim()) + '</li>');
			continue;
		}
		closeList();
		para.push(line.trim());
	}
	if (fence) out.push(`<pre><code>${esc(fence.join('\n'))}</code></pre>`);
	flushPara();
	closeList();
	return out.join('\n');
}

/**
 * A release body without its first heading when that only repeats the
 * version and date (semantic-release's "## [1.2.0](compare) (date)"): the
 * page shows those already.
 */
export function withoutTitle(body: string, version: string): string {
	const lines = (body ?? '').replace(/\r\n?/g, '\n').split('\n');
	const i = lines.findIndex((l) => l.trim() !== '');
	if (i >= 0 && /^#{1,6}\s/.test(lines[i]) && lines[i].includes(version)) {
		const rest = lines[i].replace(/^#+\s*/, '').replace(/\[[^\]]*\]\([^)]*\)|\([^)]*\)|[\d.v]+/g, '');
		if (!rest.trim()) lines.splice(i, 1);
	}
	return lines.join('\n');
}

/** How many releases behind, in words. */
export function behind(c: ComponentStatus): string {
	const n = c.releases.length;
	return n === 1 ? '1 release behind' : `${n} releases behind`;
}

/** Components the admin can tick. */
export function selectable(st: UpdatesStatus | null): ComponentStatus[] {
	return (st?.components ?? []).filter((c) => c.update && c.applyable);
}
export type ApplyMode = 'all' | 'selected' | 'quiet';

/**
 * Where the version check stands, for what the Available section shows:
 * waiting (none has finished and none is running: the panel checks a few
 * seconds after it starts), checking (the first one is running), failed
 * (it finished without finding an update but with an error, or every part
 * of the install errored: "nothing to update" would be a guess) or done.
 */
export type CheckPhase = 'waiting' | 'checking' | 'failed' | 'done';

export function checkPhase(st: UpdatesStatus | null | undefined, checkingHere = false): CheckPhase {
	if (!st?.checked_at) return st?.checking || checkingHere ? 'checking' : 'waiting';
	return checkError(st) ? 'failed' : 'done';
}

/** Why a finished check can't say whether anything needs updating ('' if it can). */
export function checkError(st: UpdatesStatus | null | undefined): string {
	if (!st?.checked_at) return '';
	const comps = st.components ?? [];
	if (comps.some((c) => c.update)) return '';
	if (st.error) return st.error;
	const present = comps.filter((c) => c.present);
	if (present.length && present.every((c) => c.error)) return present[0].error ?? '';
	return '';
}

/** A check is due or running: the page polls until its result is in. */
export function checkPending(st: UpdatesStatus | null | undefined): boolean {
	return !!st && (st.checking || !st.checked_at);
}
