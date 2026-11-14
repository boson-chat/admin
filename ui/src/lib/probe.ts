// Latency measured from the admin's browser to each service's public
// endpoint (PUBLIC_PROBE_TARGETS), next to the panel's internal checks.

import type { PublicProbe } from './types';

export interface BrowserLatency {
	/** Median round trip in ms; null when the endpoint couldn't be reached. */
	ms: number | null;
	samples: number;
	error?: string;
	at: number;
}

const TIMEOUT = 5000;
/** HTTP: the first request pays for DNS + TCP + TLS; it's a warm-up. */
export const HTTP_SAMPLES = 5;
/** HTTP probes run at most this often (the overview polls every 15 s). */
export const HTTP_EVERY = 14_000;
/**
 * WebSocket: every sample is a new IRC connection, and IRC servers throttle
 * connections per IP (Ergo's default: 32 per 10 minutes, then a ban). So one
 * open per probe, at most every 2 minutes on the poll and every 30 s on
 * "Check now".
 */
export const WS_EVERY = 120_000;
export const WS_EVERY_FORCED = 30_000;

export function median(xs: number[]): number | null {
	if (!xs.length) return null;
	const s = [...xs].sort((a, b) => a - b);
	const m = s.length >> 1;
	return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
}

async function httpOnce(url: string): Promise<number> {
	const t0 = performance.now();
	// no-cors: health endpoints don't send CORS headers; an opaque response
	// still times the round trip, and network failures still throw.
	await fetch(url, {
		mode: 'no-cors',
		cache: 'no-store',
		credentials: 'omit',
		signal: AbortSignal.timeout(TIMEOUT)
	});
	return performance.now() - t0;
}

function wsOnce(url: string): Promise<number> {
	return new Promise((resolve, reject) => {
		const t0 = performance.now();
		let ws: WebSocket;
		try {
			ws = new WebSocket(url);
		} catch (e) {
			reject(e);
			return;
		}
		const timer = setTimeout(() => {
			ws.close();
			reject(new Error('timed out'));
		}, TIMEOUT);
		ws.onopen = () => {
			const dt = performance.now() - t0;
			clearTimeout(timer);
			ws.close(1000);
			resolve(dt);
		};
		ws.onerror = () => {
			clearTimeout(timer);
			reject(new Error('connection failed'));
		};
	});
}

/** Times one endpoint: HTTP = median of 4 after a warm-up; WebSocket = one open. */
export async function measure(p: PublicProbe): Promise<BrowserLatency> {
	const at = Date.now();
	try {
		if (p.kind === 'ws') {
			const ms = await wsOnce(p.url);
			return { ms, samples: 1, at };
		}
		const times: number[] = [];
		for (let i = 0; i < HTTP_SAMPLES; i++) {
			const t = await httpOnce(p.url);
			if (i > 0) times.push(t);
		}
		return { ms: median(times), samples: times.length, at };
	} catch (e) {
		const msg = e instanceof Error ? e.message : String(e);
		return { ms: null, samples: 0, error: /abort|timeout/i.test(msg) ? 'timed out' : 'unreachable', at };
	}
}

/** Which probes are due: HTTP every ~15 s, WebSocket far less often. */
export function due(
	probes: PublicProbe[],
	last: Record<string, BrowserLatency | undefined>,
	now: number,
	forced: boolean
): PublicProbe[] {
	return probes.filter((p) => {
		const prev = last[p.name];
		if (!prev) return true;
		const age = now - prev.at;
		if (p.kind === 'ws') return age >= (forced ? WS_EVERY_FORCED : WS_EVERY);
		return age >= (forced ? 0 : HTTP_EVERY);
	});
}
