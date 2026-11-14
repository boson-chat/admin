import { describe, expect, it, vi } from 'vitest';
import {
	CompilerError,
	mjmlCompiler,
	type CompileReply,
	type CompileRequest,
	type WorkerLike
} from './compiler';
import { compile, type MjmlFn } from './compile';

/** A worker that runs compile() on a fake mjml, answering when told. */
class FakeWorker implements WorkerLike {
	onmessage: ((e: MessageEvent<CompileReply>) => void) | null = null;
	onerror: ((e: ErrorEvent) => void) | null = null;
	inbox: CompileRequest[] = [];
	terminated = false;
	constructor(private mjml: MjmlFn) {}
	postMessage(m: CompileRequest) {
		this.inbox.push(m);
	}
	terminate() {
		this.terminated = true;
	}
	/** Answers the oldest request (or the one with id). */
	async reply(id?: number) {
		const i = id === undefined ? 0 : this.inbox.findIndex((m) => m.id === id);
		const [m] = this.inbox.splice(i, 1);
		const result = await compile(this.mjml, m.source, m.part);
		this.onmessage?.({ data: { id: m.id, result } } as MessageEvent<CompileReply>);
	}
	crash(message: string) {
		this.onerror?.({ message, preventDefault() {} } as ErrorEvent);
	}
}

const mjml: MjmlFn = async (src) => ({ html: `<html>${src}</html>`, errors: [] });

describe('mjmlCompiler', () => {
	it('starts the worker on the first compile and matches answers to requests', async () => {
		const workers: FakeWorker[] = [];
		const make = vi.fn(() => {
			const w = new FakeWorker(mjml);
			workers.push(w);
			return w;
		});
		const c = mjmlCompiler(make);
		expect(make).not.toHaveBeenCalled();
		const a = c.compile('<mjml>{{content}}</mjml>', 'layout');
		const b = c.compile('<mjml>{{link}}</mjml>', 'invite');
		expect(make).toHaveBeenCalledTimes(1);
		expect(c.pending).toBe(2);
		// Answered out of order: each promise still gets its own.
		await workers[0].reply(2);
		await workers[0].reply(1);
		await expect(a).resolves.toMatchObject({ ok: true, html: '<html><mjml>{{content}}</mjml></html>' });
		await expect(b).resolves.toMatchObject({ ok: true, html: '<html><mjml>{{link}}</mjml></html>' });
		expect(c.pending).toBe(0);
	});

	it('passes the worker’s error on', async () => {
		const w = new FakeWorker(mjml);
		const c = mjmlCompiler(() => w);
		const p = c.compile('<mjml/>', 'layout');
		w.onmessage?.({ data: { id: 1, error: 'mjml-browser didn’t load' } } as MessageEvent<CompileReply>);
		await expect(p).rejects.toThrow('mjml-browser didn’t load');
		await expect(p).rejects.toBeInstanceOf(CompilerError);
	});

	it('drops an aborted compile', async () => {
		const w = new FakeWorker(mjml);
		const c = mjmlCompiler(() => w);
		const ctrl = new AbortController();
		const p = c.compile('<mjml>{{content}}</mjml>', 'layout', ctrl.signal);
		ctrl.abort();
		await expect(p).rejects.toMatchObject({ name: 'AbortError' });
		await w.reply(); // the late answer goes nowhere
		expect(c.pending).toBe(0);
		await expect(c.compile('x', 'layout', ctrl.signal)).rejects.toMatchObject({ name: 'AbortError' });
	});

	it('fails what’s pending when the worker crashes, then starts a new one', async () => {
		const workers: FakeWorker[] = [];
		const c = mjmlCompiler(() => {
			const w = new FakeWorker(mjml);
			workers.push(w);
			return w;
		});
		const p = c.compile('<mjml/>', 'layout');
		workers[0].crash('out of memory');
		await expect(p).rejects.toThrow('the MJML compiler stopped: out of memory');
		expect(workers[0].terminated).toBe(true);
		const q = c.compile('<mjml>{{content}}</mjml>', 'layout');
		expect(workers).toHaveLength(2);
		await workers[1].reply();
		await expect(q).resolves.toMatchObject({ ok: true });
		c.dispose();
		expect(workers[1].terminated).toBe(true);
	});
});
