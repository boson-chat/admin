import { describe, expect, it } from 'vitest';
import { due, median, HTTP_EVERY, WS_EVERY, WS_EVERY_FORCED } from './probe';
import type { PublicProbe } from './types';

describe('median', () => {
	it('handles odd, even and empty', () => {
		expect(median([30, 10, 20])).toBe(20);
		expect(median([40, 10, 30, 20])).toBe(25);
		expect(median([])).toBeNull();
	});
});

describe('due', () => {
	const probes: PublicProbe[] = [
		{ name: 'auth', url: 'https://auth.example.com/healthz', kind: 'http' },
		{ name: 'irc', url: 'wss://irc.example.com/webirc', kind: 'ws' }
	];
	const at = 1_000_000;
	const last = { auth: { ms: 1, samples: 4, at }, irc: { ms: 1, samples: 1, at } };
	const names = (ps: PublicProbe[]) => ps.map((p) => p.name);

	it('runs everything the first time', () => {
		expect(names(due(probes, {}, at, false))).toEqual(['auth', 'irc']);
	});
	it('re-times HTTP each poll but opens WebSockets rarely', () => {
		expect(names(due(probes, last, at + HTTP_EVERY - 1, false))).toEqual([]);
		expect(names(due(probes, last, at + HTTP_EVERY, false))).toEqual(['auth']);
		expect(names(due(probes, last, at + WS_EVERY, false))).toEqual(['auth', 'irc']);
	});
	it('"Check now" forces HTTP, and WebSockets after a shorter floor', () => {
		expect(names(due(probes, last, at + 1, true))).toEqual(['auth']);
		expect(names(due(probes, last, at + WS_EVERY_FORCED, true))).toEqual(['auth', 'irc']);
	});
});
