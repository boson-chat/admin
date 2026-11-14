import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { brandingPreviews, frameDoc, livePreview, previewField, previewRequest } from './livepreview';

/** A fetcher whose calls the test resolves by hand. */
function controlled() {
	const calls: {
		input: string;
		signal: AbortSignal;
		resolve: (v: string) => void;
		reject: (e: unknown) => void;
	}[] = [];
	const fetch = (input: string, signal: AbortSignal) =>
		new Promise<string>((resolve, reject) => {
			calls.push({ input, signal, resolve, reject });
		});
	return { calls, fetch };
}

function setup(fetch: (i: string, s: AbortSignal) => Promise<string>) {
	const results: string[] = [];
	const errors: unknown[] = [];
	const loading: boolean[] = [];
	const lp = livePreview<string, string>({
		fetch,
		onResult: (o) => results.push(o),
		onError: (e) => errors.push(e),
		onLoading: (b) => loading.push(b),
		delay: 300
	});
	return { lp, results, errors, loading };
}

describe('livePreview', () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it('debounces: only the last input after a 300 ms pause is fetched', async () => {
		const c = controlled();
		const { lp, results } = setup(c.fetch);
		lp.schedule('a');
		vi.advanceTimersByTime(200);
		lp.schedule('ab');
		vi.advanceTimersByTime(200);
		lp.schedule('abc');
		vi.advanceTimersByTime(299);
		expect(c.calls).toHaveLength(0);
		vi.advanceTimersByTime(1);
		expect(c.calls.map((x) => x.input)).toEqual(['abc']);
		c.calls[0].resolve('ABC');
		await vi.runAllTimersAsync();
		expect(results).toEqual(['ABC']);
	});

	it('now() skips the wait and the pending debounce', async () => {
		const c = controlled();
		const { lp } = setup(c.fetch);
		lp.schedule('typed');
		void lp.now('tab');
		expect(c.calls.map((x) => x.input)).toEqual(['tab']);
		vi.advanceTimersByTime(1000);
		expect(c.calls).toHaveLength(1);
	});

	it('aborts the request in flight when a newer one starts', async () => {
		const c = controlled();
		const { lp, results, errors } = setup((i, s) => {
			const p = c.fetch(i, s);
			// Like fetch(): an abort rejects with an AbortError.
			s.addEventListener('abort', () => c.calls.at(-1)!.reject(new DOMException('aborted', 'AbortError')));
			return p;
		});
		void lp.now('one');
		void lp.now('two');
		expect(c.calls[0].signal.aborted).toBe(true);
		expect(c.calls[1].signal.aborted).toBe(false);
		c.calls[1].resolve('TWO');
		await vi.runAllTimersAsync();
		expect(results).toEqual(['TWO']);
		expect(errors).toEqual([]); // the abort isn't an error
	});

	it('drops a stale response that arrives after a newer one', async () => {
		const c = controlled(); // ignores the signal, like a slow server
		const { lp, results, errors, loading } = setup(c.fetch);
		const first = lp.now('one');
		const second = lp.now('two');
		c.calls[1].resolve('TWO');
		await second;
		c.calls[0].resolve('ONE');
		await first;
		expect(results).toEqual(['TWO']);
		// A stale failure is dropped too.
		const third = lp.now('three');
		const fourth = lp.now('four');
		c.calls[3].resolve('FOUR');
		c.calls[2].reject(new Error('late'));
		await Promise.all([third, fourth]);
		expect(results).toEqual(['TWO', 'FOUR']);
		expect(errors).toEqual([]);
		expect(loading.at(-1)).toBe(false);
	});

	it('reports the latest error and stops loading', async () => {
		const { lp, results, errors, loading } = setup(() => Promise.reject(new Error('bad accent')));
		await lp.now('x');
		expect(results).toEqual([]);
		expect((errors[0] as Error).message).toBe('bad accent');
		expect(loading).toEqual([true, false]);
	});

	it('cancel() stops a pending and a running render', async () => {
		const c = controlled();
		const { lp, results } = setup(c.fetch);
		lp.schedule('a');
		expect(lp.pending).toBe(true);
		lp.cancel();
		vi.advanceTimersByTime(1000);
		expect(c.calls).toHaveLength(0);
		const run = lp.now('b');
		lp.cancel();
		expect(c.calls[0].signal.aborted).toBe(true);
		c.calls[0].resolve('B');
		await run;
		expect(results).toEqual([]);
		expect(lp.pending).toBe(false);
	});
});

describe('email preview helpers', () => {
	it('sends the on-screen settings and only non-empty wording', () => {
		const s = { sender_name: ' Example ', accent: null, footer: ' Bye ', logo: false };
		expect(previewRequest('invite', { subject: '  ', body: '' }, s, '#3b5bdb')).toEqual({
			template: 'invite',
			email: { sender_name: 'Example', accent: '#3b5bdb', footer: 'Bye', logo: false }
		});
		const r = previewRequest(
			'invite',
			{ subject: ' Hi ', body: 'Body\n' },
			{ ...s, accent: '#7c3aed' },
			'#000000'
		);
		expect(r.subject).toBe('Hi');
		expect(r.body).toBe('Body\n');
		expect(r.email.accent).toBe('#7c3aed');
	});

	it('tells an auth without branding previews by the missing from', () => {
		expect(brandingPreviews({ subject: '', html: '', text: '' })).toBe(false);
		expect(brandingPreviews({ subject: '', html: '', text: '', from: 'A <a@b.c>' })).toBe(true);
	});

	it('maps error fields to inputs', () => {
		expect(previewField('email.accent')).toBe('accent');
		expect(previewField('email.sender_name')).toBe('sender_name');
		expect(previewField('subject')).toBe('subject');
		expect(previewField('template')).toBeUndefined();
		expect(previewField(undefined)).toBeUndefined();
	});

	it('pins the email’s colour-scheme media queries to the chosen scheme', () => {
		const html =
			'<html><head><style>@media (prefers-color-scheme: dark){.a{color:#fff}} @media (prefers-color-scheme:light){.b{}}</style></head><body>x</body></html>';
		const dark = frameDoc(html, 'dark');
		expect(dark).toContain('<meta name="color-scheme" content="dark">');
		expect(dark).toContain('@media (min-width: 0px){.a');
		expect(dark).toContain('@media (max-width: 0px){.b');
		const light = frameDoc(html, 'light');
		expect(light).toContain('content="light"');
		expect(light).toContain('@media (max-width: 0px){.a');
		expect(light).not.toContain('prefers-color-scheme');
		// A fragment gets a document around it.
		expect(frameDoc('<p>hi</p>', 'dark')).toMatch(/^<!doctype html><html><head><meta name="color-scheme"/);
	});
});
