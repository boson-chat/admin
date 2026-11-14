import { isAbort } from './api';

/**
 * Re-renders something remote as its inputs change: schedule() waits for
 * the typing to settle (debounce), now() runs at once. A newer run aborts
 * the one in flight, and a response that still arrives late (a fetcher that
 * ignores the signal) is dropped by its sequence number, so only the
 * latest input's result or error is ever reported.
 */
export function livePreview<I, O>(o: {
	fetch: (input: I, signal: AbortSignal) => Promise<O>;
	onResult: (out: O, input: I) => void;
	onError: (err: unknown, input: I) => void;
	onLoading?: (loading: boolean) => void;
	delay?: number;
}) {
	const delay = o.delay ?? 300;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let ctrl: AbortController | undefined;
	let seq = 0;

	async function run(input: I) {
		clearTimeout(timer);
		timer = undefined;
		ctrl?.abort();
		const c = new AbortController();
		ctrl = c;
		const id = ++seq;
		o.onLoading?.(true);
		try {
			const out = await o.fetch(input, c.signal);
			if (id === seq) o.onResult(out, input);
		} catch (e) {
			if (id === seq && !isAbort(e)) o.onError(e, input);
		} finally {
			if (id === seq) {
				ctrl = undefined;
				o.onLoading?.(false);
			}
		}
	}

	return {
		schedule(input: I) {
			clearTimeout(timer);
			timer = setTimeout(() => void run(input), delay);
		},
		now: run,
		/** Stops a pending or running render; nothing more is reported. */
		cancel() {
			clearTimeout(timer);
			timer = undefined;
			ctrl?.abort();
			ctrl = undefined;
			seq++;
			o.onLoading?.(false);
		},
		get pending() {
			return timer !== undefined || ctrl !== undefined;
		}
	};
}

export type EmailSettings = { sender_name: string; accent: string | null; footer: string; logo: boolean };
export type PreviewRequest = {
	template: string;
	subject?: string;
	body?: string;
	email: { sender_name: string; accent: string; footer: string; logo: boolean };
	/** Compiled MJML not saved yet: the layout, or this email's own. */
	layout_html?: string;
	html?: string;
};
export type EmailPreview = {
	subject: string;
	html: string;
	text: string;
	from?: string;
	/** auth's notes on custom HTML (an unknown placeholder, say). */
	warnings?: string[];
};

/**
 * The preview request for what's on screen. The settings always go along
 * (an empty sender name or footer means auth's default, like saving it
 * empty); "no colour" is sent as auth's default accent.
 */
export function previewRequest(
	template: string,
	draft: { subject: string; body: string },
	s: EmailSettings,
	defaultAccent: string
): PreviewRequest {
	const r: PreviewRequest = {
		template,
		email: {
			sender_name: s.sender_name.trim(),
			accent: s.accent ?? defaultAccent,
			footer: s.footer.trim(),
			logo: s.logo
		}
	};
	if (draft.subject.trim()) r.subject = draft.subject.trim();
	if (draft.body.trim()) r.body = draft.body;
	return r;
}

/** An auth from before the MJML layout ignores the settings and sends no from. */
export const brandingPreviews = (p: EmailPreview) => !!p.from;

/** Preview request fields → the input they belong to. */
export type PreviewField = 'subject' | 'body' | 'sender_name' | 'accent' | 'footer' | 'logo' | 'mjml';
export function previewField(f: string | undefined): PreviewField | undefined {
	switch (f) {
		case 'subject':
		case 'body':
			return f;
		// The compiled MJML on screen: the message goes under the editor.
		case 'layout_html':
		case 'html':
			return 'mjml';
		case 'email.sender_name':
		case 'email.accent':
		case 'email.footer':
		case 'email.logo':
			return f.slice(6) as PreviewField;
	}
	return undefined;
}

/**
 * The email's HTML for the preview frame in a light or dark client: the
 * colour scheme is declared (so the canvas and defaults follow), and the
 * email's own prefers-color-scheme media queries are pinned to the chosen
 * scheme, so they don't depend on the browser's support for emulating it
 * inside an iframe.
 */
export function frameDoc(html: string, scheme: 'light' | 'dark'): string {
	const on = '(min-width: 0px)';
	const off = '(max-width: 0px)';
	const pinned = html.replace(/\(\s*prefers-color-scheme\s*:\s*(light|dark)\s*\)/gi, (_, s: string) =>
		s.toLowerCase() === scheme ? on : off
	);
	const head =
		`<meta name="color-scheme" content="${scheme}">` + `<style>:root{color-scheme:${scheme}}</style>`;
	if (/<head[^>]*>/i.test(pinned)) return pinned.replace(/<head[^>]*>/i, (m) => m + head);
	if (/<html[^>]*>/i.test(pinned))
		return pinned.replace(/<html[^>]*>/i, (m) => m + '<head>' + head + '</head>');
	return '<!doctype html><html><head>' + head + '</head><body>' + pinned + '</body></html>';
}
