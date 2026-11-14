import type { CompileResult, Part } from './compile';

export type CompileRequest = { id: number; source: string; part: Part };
export type CompileReply = { id: number; result?: CompileResult; error?: string };

/** What the compiler needs from a Worker (a fake one in tests). */
export type WorkerLike = {
	postMessage(m: CompileRequest): void;
	terminate(): void;
	onmessage: ((e: MessageEvent<CompileReply>) => void) | null;
	onerror: ((e: ErrorEvent) => void) | null;
};

/** The worker couldn't run (it failed to load, or mjml-browser did). */
export class CompilerError extends Error {}

/**
 * Talks to the MJML worker: compile() resolves with the result for that
 * source. The worker starts on the first compile; a crashed worker fails
 * what's pending and is started again next time.
 */
export function mjmlCompiler(make: () => WorkerLike) {
	let worker: WorkerLike | undefined;
	let next = 0;
	const pending = new Map<number, { resolve: (r: CompileResult) => void; reject: (e: Error) => void }>();

	function fail(msg: string) {
		for (const p of pending.values()) p.reject(new CompilerError(msg));
		pending.clear();
		worker?.terminate();
		worker = undefined;
	}

	function start(): WorkerLike {
		if (worker) return worker;
		const w = make();
		w.onmessage = (e) => {
			const p = pending.get(e.data.id);
			if (!p) return;
			pending.delete(e.data.id);
			if (e.data.result) p.resolve(e.data.result);
			else p.reject(new CompilerError(e.data.error || 'the MJML compiler failed'));
		};
		w.onerror = (e) => {
			e.preventDefault?.();
			fail(e.message ? `the MJML compiler stopped: ${e.message}` : 'the MJML compiler couldn’t start');
		};
		worker = w;
		return w;
	}

	return {
		compile(source: string, part: Part, signal?: AbortSignal): Promise<CompileResult> {
			return new Promise((resolve, reject) => {
				if (signal?.aborted) return reject(new DOMException('aborted', 'AbortError'));
				const id = ++next;
				pending.set(id, { resolve, reject });
				signal?.addEventListener(
					'abort',
					() => {
						// The worker finishes anyway; its answer is dropped.
						if (pending.delete(id)) reject(new DOMException('aborted', 'AbortError'));
					},
					{ once: true }
				);
				start().postMessage({ id, source, part });
			});
		},
		get pending() {
			return pending.size;
		},
		dispose() {
			fail('closed');
		}
	};
}

export type MjmlCompiler = ReturnType<typeof mjmlCompiler>;

/** The real worker: a bundled file served from the panel (worker-src 'self'). */
export const browserWorker = (): WorkerLike =>
	new Worker(new URL('./mjml.worker.ts', import.meta.url), { name: 'mjml' }) as unknown as WorkerLike;
