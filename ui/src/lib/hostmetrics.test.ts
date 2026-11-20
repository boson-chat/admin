import { describe, expect, it } from 'vitest';
import {
	axis,
	bytes,
	dataMax,
	derive,
	duration,
	isRange,
	level,
	levelWord,
	loadNum,
	nearest,
	pct,
	rate,
	sample,
	segments,
	stack,
	targetLevel,
	targetSummary,
	tickLabel,
	usedPct,
	xTicks
} from './hostmetrics';
import type { HostStats } from './types';

const stats = (o: Partial<HostStats> = {}): HostStats => ({
	uptime_seconds: 3600,
	cores: 2,
	cpu_percent: 10,
	mem_total_bytes: 2 * 1024 ** 3,
	mem_available_bytes: 0.5 * 1024 ** 3,
	swap_total_bytes: 1024 ** 3,
	swap_free_bytes: 1024 ** 3,
	load1: 1,
	load5: 0.5,
	load15: 0.25,
	disk_busy_percent: 3,
	pressure_cpu: null,
	pressure_memory: null,
	pressure_io: null,
	...o
});

describe('formatting', () => {
	it('formats bytes in powers of 1024', () => {
		expect(bytes(512)).toBe('512 B');
		expect(bytes(1536)).toBe('1.5 KB');
		expect(bytes(2 * 1024 ** 3)).toBe('2.0 GB');
		expect(bytes(150 * 1024 ** 2)).toBe('150 MB');
		expect(bytes(null)).toBe('—');
		expect(rate(1024 * 1024)).toBe('1.0 MB/s');
	});
	it('formats percentages, load and durations', () => {
		expect(pct(37.4)).toBe('37%');
		expect(pct(4.25)).toBe('4.3%');
		expect(pct(0.04)).toBe('<0.1%');
		expect(pct(0)).toBe('0%');
		expect(pct(null)).toBe('—');
		expect(loadNum(2.456)).toBe('2.46');
		expect(loadNum(12.34)).toBe('12.3');
		expect(duration(3 * 86400 + 4 * 3600 + 120)).toBe('3d 4h');
		expect(duration(5 * 3600 + 12 * 60)).toBe('5h 12m');
		expect(duration(42 * 60)).toBe('42m');
		expect(duration(null)).toBe('—');
	});
	it('labels axis ticks compactly', () => {
		expect(tickLabel('percent', 50)).toBe('50%');
		expect(tickLabel('bytes_per_sec', 0)).toBe('0');
		expect(tickLabel('bytes_per_sec', 512 * 1024)).toBe('512KB');
		expect(tickLabel('load', 1.5)).toBe('1.5');
	});
	it('knows the ranges', () => {
		expect(isRange('24h')).toBe(true);
		expect(isRange('1y')).toBe(false);
		expect(isRange(null)).toBe(false);
	});
});

describe('derive and thresholds', () => {
	it('derives memory, swap and load per core', () => {
		const d = derive(stats({ swap_free_bytes: 0.75 * 1024 ** 3 }));
		expect(d.memUsedPct).toBe(75);
		expect(d.memUsed).toBe(1.5 * 1024 ** 3);
		expect(d.swapUsedPct).toBe(25);
		expect(d.loadPerCore).toBe(0.5);
	});
	it('has no swap percentage without swap or data', () => {
		expect(derive(stats({ swap_total_bytes: 0, swap_free_bytes: 0 })).swapUsedPct).toBeNull();
		expect(derive(stats({ mem_available_bytes: null })).memUsedPct).toBeNull();
		expect(derive(stats({ cores: null })).loadPerCore).toBeNull();
	});
	it('flags disk over 85%, memory over 90% and any swap', () => {
		expect(level('disk', 84.9)).toBe('ok');
		expect(level('disk', 85)).toBe('warn');
		expect(level('disk', 96)).toBe('bad');
		expect(level('memory', 79)).toBe('ok');
		expect(level('memory', 85)).toBe('warn');
		expect(level('memory', 90)).toBe('bad');
		expect(level('swap', 0)).toBe('ok');
		expect(level('swap', 0.2)).toBe('ok');
		expect(level('swap', 3)).toBe('warn');
		expect(level('swap', 60)).toBe('bad');
		expect(level('load', 0.9)).toBe('ok');
		expect(level('load', 1.2)).toBe('warn');
		expect(level('cpu', null)).toBe('ok');
	});
	it('names every non-ok level', () => {
		expect(levelWord('swap', 'warn')).toBe('In use');
		expect(levelWord('disk', 'bad')).toBe('Nearly full');
		expect(levelWord('memory', 'bad')).toBe('Critical');
		expect(levelWord('cpu', 'ok')).toBe('');
	});
	it('computes used percent', () => {
		expect(usedPct(100, 10)).toBe(90);
		expect(usedPct(0, 0)).toBe(0);
	});
});

describe('service health', () => {
	const t = (job: string, status: 'up' | 'down' | 'stale') => ({ job, instance: job + ':80', status });
	it('summarises targets', () => {
		expect(targetSummary([])).toBe('No scrape targets reported.');
		expect(targetSummary([t('auth', 'up'), t('admin', 'up')])).toBe('All 2 targets are up.');
		expect(targetSummary([t('auth', 'down'), t('admin', 'up')])).toBe('auth is not answering.');
		expect(targetSummary([t('auth', 'down'), t('traefik', 'stale')])).toBe(
			'auth, traefik are not answering.'
		);
	});
	it('maps states to levels', () => {
		expect(targetLevel(t('a', 'up'))).toBe('ok');
		expect(targetLevel(t('a', 'stale'))).toBe('warn');
		expect(targetLevel(t('a', 'down'))).toBe('bad');
	});
});

describe('chart geometry', () => {
	it('builds round y-axes', () => {
		expect(axis(2.67, 'load')).toEqual({ max: 3, ticks: [0, 1, 2, 3] });
		expect(axis(42, 'percent', 100).ticks).toEqual([0, 25, 50, 75, 100]);
		const MB = 1024 * 1024;
		expect(axis(2.4 * MB, 'bytes_per_sec')).toEqual({ max: 3 * MB, ticks: [0, MB, 2 * MB, 3 * MB] });
		expect(axis(760 * 1024, 'bytes_per_sec').max).toBe(800 * 1024);
		expect(axis(0, 'load').max).toBe(1);
		expect(tickLabel('bytes_per_sec', MB)).toBe('1MB');
		expect(tickLabel('bytes_per_sec', 2.5 * MB)).toBe('2.5MB');
	});
	it('splits lines at missing points', () => {
		const segs = segments(
			[
				[0, 1],
				[15, 1],
				[30, 1],
				[90, 2],
				[105, 2]
			],
			15
		);
		expect(segs.map((s) => s.length)).toEqual([3, 2]);
	});
	it('stacks series on shared timestamps', () => {
		const st = stack([
			{
				name: 'user',
				points: [
					[0, 10],
					[15, 20]
				]
			},
			{ name: 'system', points: [[15, 5]] }
		]);
		expect(st[1]).toEqual([
			[0, 10, 10],
			[15, 20, 25]
		]);
		expect(
			dataMax(
				[
					{
						name: 'a',
						points: [
							[0, 10],
							[15, 20]
						]
					},
					{ name: 'b', points: [[15, 5]] }
				],
				true
			)
		).toBe(25);
		expect(
			dataMax(
				[
					{
						name: 'a',
						points: [
							[0, 10],
							[15, 20]
						]
					}
				],
				false
			)
		).toBe(20);
	});
	it('finds the nearest point', () => {
		expect(nearest([0, 15, 30, 45], 20)).toBe(1);
		expect(nearest([0, 15, 30, 45], 23)).toBe(2);
		expect(nearest([0, 15, 30, 45], 999)).toBe(3);
		expect(nearest([], 1)).toBe(-1);
	});
	it('places round time ticks', () => {
		const start = Date.parse('2026-11-20T10:07:00Z') / 1000;
		const ticks = xTicks(start, start + 3600, 6);
		expect(ticks.length).toBeGreaterThanOrEqual(3);
		expect(ticks.length).toBeLessThanOrEqual(6);
		for (const t of ticks) expect(t % 600).toBe(0);
	});
	it('samples table rows evenly, keeping both ends', () => {
		const rows = Array.from({ length: 240 }, (_, i) => i);
		const s = sample(rows, 12);
		expect(s.length).toBe(12);
		expect(s[0]).toBe(0);
		expect(s[11]).toBe(239);
		expect(sample([1, 2], 12)).toEqual([1, 2]);
	});
});
