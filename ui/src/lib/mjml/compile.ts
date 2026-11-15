/**
 * MJML → HTML for the Emails page (branding contract, section 6). The
 * compiler itself (mjml-browser) is passed in, so this runs the same in
 * the worker and in tests: a structure check of the source first (so an
 * unclosed tag gets a line number instead of silently odd output), then
 * MJML's own validation, then the placeholder rules auth enforces.
 */

export type Severity = 'error' | 'warning';
export type Diagnostic = { line: number; message: string; severity: Severity };
export type CompileResult = {
	/** Empty when the source couldn't be compiled at all. */
	html: string;
	diagnostics: Diagnostic[];
	/** No errors: the HTML may be saved. */
	ok: boolean;
};

/** mjml-browser's default export (async from 5.x). */
export type MjmlFn = (
	source: string,
	options: Record<string, unknown>
) => Promise<{ html: string; errors?: MjmlError[] }> | { html: string; errors?: MjmlError[] };
type MjmlError = { line?: number; message?: string; tagName?: string; formattedMessage?: string };

/** "layout" or a template id. */
export type Part = string;
export const LAYOUT = 'layout';

/** Placeholders auth fills (section 6), in the order the chips show them. */
export const PLACEHOLDERS = [
	'content',
	'subject',
	'network',
	'username',
	'inviter',
	'expires',
	'link',
	'logo_url',
	'accent',
	'accent_text',
	'footer',
	'sender_name'
] as const;
export const PLACEHOLDER_HELP: Record<string, string> = {
	content: 'The email’s text and button (layout only, exactly once)',
	subject: 'The subject line',
	network: 'The network’s name',
	username: 'The account’s username',
	inviter: 'Who sent the invite',
	expires: 'How long the link works',
	link: 'The link to click (a URL)',
	logo_url: 'The logo’s URL',
	accent: 'The accent colour, #rrggbb',
	accent_text: 'A text colour readable on the accent (for button labels)',
	footer: 'The footer line',
	sender_name: 'The sender name'
};
/** The placeholders a part may use. */
export const placeholdersFor = (part: Part) =>
	part === LAYOUT ? [...PLACEHOLDERS] : PLACEHOLDERS.filter((p) => p !== 'content');

/** 1-based line of offset i in s. */
export function lineAt(s: string, i: number): number {
	let n = 1;
	for (let j = 0; j < i && j < s.length; j++) if (s.charCodeAt(j) === 10) n++;
	return n;
}

/**
 * MJML tags whose content is HTML (or CSS), not more MJML: the checker
 * skips to their closing tag.
 */
const ENDING = new Set([
	'mj-text',
	'mj-button',
	'mj-raw',
	'mj-table',
	'mj-navbar-link',
	'mj-accordion-title',
	'mj-accordion-text',
	'mj-social-element',
	'mj-style',
	'mj-title',
	'mj-preview'
]);
const VOID = new Set([
	'area',
	'base',
	'br',
	'col',
	'embed',
	'hr',
	'img',
	'input',
	'link',
	'meta',
	'param',
	'source',
	'track',
	'wbr'
]);

/**
 * Checks the tag structure: every tag closed, in order, one <mjml> root.
 * Returns errors with the line they're on.
 */
export function checkStructure(src: string): Diagnostic[] {
	const out: Diagnostic[] = [];
	const err = (i: number, message: string) => out.push({ line: lineAt(src, i), message, severity: 'error' });
	const stack: { name: string; at: number }[] = [];
	let root = '';
	let rootAt = -1;
	let i = 0;
	while (i < src.length) {
		const lt = src.indexOf('<', i);
		if (lt < 0) break;
		if (src.startsWith('<!--', lt)) {
			const end = src.indexOf('-->', lt + 4);
			if (end < 0) {
				err(lt, 'This comment is never closed (-->).');
				return out;
			}
			i = end + 3;
			continue;
		}
		if (src.startsWith('<![CDATA[', lt)) {
			const end = src.indexOf(']]>', lt);
			i = end < 0 ? src.length : end + 3;
			continue;
		}
		if (src[lt + 1] === '?' || src[lt + 1] === '!') {
			const end = src.indexOf('>', lt);
			i = end < 0 ? src.length : end + 1;
			continue;
		}
		const m = /^<(\/?)([A-Za-z][\w:.-]*)/.exec(src.slice(lt, lt + 80));
		if (!m) {
			i = lt + 1;
			continue;
		}
		const closing = m[1] === '/';
		const name = m[2].toLowerCase();
		// The end of the tag, skipping quoted attribute values.
		let j = lt + m[0].length;
		let quote = '';
		for (; j < src.length; j++) {
			const c = src[j];
			if (quote) {
				if (c === quote) quote = '';
			} else if (c === '"' || c === "'") quote = c;
			else if (c === '>') break;
			else if (c === '<') {
				j = -1;
				break;
			}
		}
		if (j < 0 || j >= src.length) {
			err(lt, `The <${closing ? '/' : ''}${name}> tag is never finished (missing >).`);
			return out;
		}
		const selfClosing = src[j - 1] === '/';
		i = j + 1;
		if (closing) {
			const top = stack.at(-1);
			if (!top) err(lt, `</${name}> closes a tag that isn’t open.`);
			else if (top.name !== name) {
				const k = stack.findLastIndex((t) => t.name === name);
				if (k < 0) err(lt, `</${name}> closes a tag that isn’t open (expected </${top.name}>).`);
				else {
					for (const t of stack.slice(k + 1).reverse())
						err(t.at, `<${t.name}> is never closed (expected </${t.name}> before </${name}>).`);
					stack.length = k;
				}
			} else stack.pop();
			continue;
		}
		if (!stack.length) {
			if (root) err(lt, `<${name}> is outside the <mjml> root.`);
			else {
				root = name;
				rootAt = lt;
			}
		}
		if (selfClosing || VOID.has(name)) continue;
		if (ENDING.has(name)) {
			const re = new RegExp(`</${name}\\s*>`, 'i');
			const rest = src.slice(i);
			const e = re.exec(rest);
			if (!e) {
				err(lt, `<${name}> is never closed.`);
				return out;
			}
			i += e.index + e[0].length;
			continue;
		}
		stack.push({ name, at: lt });
	}
	for (const t of stack.reverse()) err(t.at, `<${t.name}> is never closed.`);
	if (!root) out.push({ line: 1, message: 'The document needs an <mjml> root element.', severity: 'error' });
	else if (root !== 'mjml') err(rootAt, `The root element must be <mjml>, not <${root}>.`);
	return out;
}

const PH = /\{\{\s*([A-Za-z_][\w]*)\s*\}\}/g;

/** The placeholder rules (section 6), as diagnostics on the source. */
export function checkPlaceholders(src: string, part: Part): Diagnostic[] {
	const out: Diagnostic[] = [];
	const allowed = placeholdersFor(part) as string[];
	const content: number[] = [];
	let link = false;
	for (const m of src.matchAll(PH)) {
		const name = m[1];
		const line = lineAt(src, m.index);
		if (name === 'content') content.push(line);
		if (name === 'link') link = true;
		if (name === 'content' && part !== LAYOUT)
			out.push({
				line,
				severity: 'warning',
				message:
					'{{content}} is only filled in the layout; in an email’s own MJML it stays as typed. Write the text here instead.'
			});
		else if (!allowed.includes(name))
			out.push({
				line,
				severity: 'warning',
				message: `{{${name}}} isn’t a placeholder auth fills; it will show as typed.`
			});
	}
	if (part === LAYOUT) {
		if (!content.length)
			out.push({
				line: 1,
				severity: 'error',
				message: 'The layout needs {{content}} once: auth puts each email’s text and button there.'
			});
		for (const line of content.slice(1))
			out.push({ line, severity: 'error', message: '{{content}} may appear only once in the layout.' });
	} else if (!link)
		out.push({
			line: 1,
			severity: 'error',
			message: 'This email needs {{link}} (the link it’s for), usually as a button’s href.'
		});
	return out;
}

const sortDiags = (d: Diagnostic[]) =>
	d.sort((a, b) => a.line - b.line || (a.severity === b.severity ? 0 : a.severity === 'error' ? -1 : 1));

/** Compiles part's MJML source with mjml. */
export async function compile(mjml: MjmlFn, src: string, part: Part): Promise<CompileResult> {
	if (!src.trim())
		return {
			html: '',
			ok: false,
			diagnostics: [{ line: 1, severity: 'error', message: 'The MJML is empty.' }]
		};
	const structure = checkStructure(src);
	const placeholders = checkPlaceholders(src, part);
	if (structure.length)
		return { html: '', ok: false, diagnostics: sortDiags([...structure, ...placeholders]) };
	let html = '';
	const diagnostics: Diagnostic[] = [];
	try {
		const r = await mjml(src, { validationLevel: 'soft', keepComments: false });
		html = r.html ?? '';
		for (const e of r.errors ?? []) {
			const message = (e.message ?? e.formattedMessage ?? 'MJML error').trim();
			// color="{{accent}}" isn't a colour to MJML, but auth fills it
			// in (and MJML passes it through); the placeholder check
			// covers unknown names.
			if (/invalid value: [^]*\{\{/.test(message)) continue;
			diagnostics.push({ line: e.line && e.line > 0 ? e.line : 1, severity: 'error', message });
		}
	} catch (e) {
		const msg = e instanceof Error ? e.message : String(e);
		const line = /line (\d+)/i.exec(msg);
		diagnostics.push({ line: line ? Number(line[1]) : 1, severity: 'error', message: msg });
	}
	const all = sortDiags([...diagnostics, ...placeholders]);
	return { html, diagnostics: all, ok: !!html && !all.some((d) => d.severity === 'error') };
}
