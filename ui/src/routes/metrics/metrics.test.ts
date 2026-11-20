import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import type { HostMetrics } from '$lib/types';

const api = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => ({ api, isAbort: () => false }));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://x/metrics?range=6h') } }));

import Page from './+page.svelte';

// jsdom has no layout (charts measure their width with bind:clientWidth).
globalThis.ResizeObserver ??= class {
	observe() {}
	unobserve() {}
	disconnect() {}
} as unknown as typeof ResizeObserver;

const base = {
	configured: true,
	checked_at: new Date().toISOString(),
	range: '6h',
	step: 90,
	start: new Date(Date.now() - 6 * 3600e3).toISOString(),
	end: new Date().toISOString(),
	instances: [],
	targets: [
		{ job: 'quicvideod', instance: 'quicvideod:8080', status: 'down', availability: 12.5 },
		{
			job: 'auth',
			instance: 'auth:8081',
			status: 'up',
			availability: 100,
			last_scrape: new Date().toISOString()
		}
	]
} as const;

function serve(v: Partial<HostMetrics>) {
	api.mockResolvedValue({ ...base, node_exporter: false, ...v });
}

const GB = 1024 ** 3;

describe('Metrics page', () => {
	beforeEach(() => api.mockReset());

	it('asks for the range in the URL', async () => {
		serve({});
		render(Page);
		await screen.findByText('No host metrics yet');
		expect(api).toHaveBeenCalledWith(
			'GET',
			'/metrics/host?range=6h',
			undefined,
			undefined,
			expect.anything()
		);
	});

	it('explains node-exporter when it is missing but still lists service health', async () => {
		serve({});
		render(Page);
		const off = await screen.findByText('No host metrics yet');
		expect(off.closest('.state')?.textContent).toContain('node-exporter');
		expect(screen.getByText('quicvideod')).toBeTruthy();
		expect(screen.getByText('Down')).toBeTruthy();
		expect(screen.getByText(/quicvideod is not answering/)).toBeTruthy();
	});

	it('says when metrics are off', async () => {
		api.mockResolvedValue({ ...base, configured: false, node_exporter: false, targets: [] });
		render(Page);
		expect(await screen.findByText('Metrics are off')).toBeTruthy();
		expect(screen.queryByText('Service health')).toBeNull();
	});

	it('shows the backend error', async () => {
		serve({ error: 'metrics backend unreachable' });
		render(Page);
		expect(await screen.findByText(/metrics backend unreachable/)).toBeTruthy();
	});

	it('highlights memory, swap and disk thresholds', async () => {
		serve({
			node_exporter: true,
			instances: ['node-exporter:9100'],
			instance: 'node-exporter:9100',
			host: {
				hostname: 'boson',
				kernel: '6.8.0',
				stats: {
					uptime_seconds: 90000,
					cores: 2,
					cpu_percent: 12,
					mem_total_bytes: 2 * GB,
					mem_available_bytes: 0.1 * GB,
					swap_total_bytes: 1 * GB,
					swap_free_bytes: 0.8 * GB,
					load1: 0.5,
					load5: 0.4,
					load15: 0.3,
					disk_busy_percent: null,
					pressure_cpu: null,
					pressure_memory: 4,
					pressure_io: null
				},
				filesystems: [
					{ mountpoint: '/', device: '/dev/sda1', fstype: 'ext4', size_bytes: 40 * GB, avail_bytes: 4 * GB }
				],
				charts: [
					{
						key: 'memory',
						title: 'Memory and swap used',
						unit: 'percent',
						max: 100,
						series: [{ name: 'Memory', points: [[Date.now() / 1000, 95]] }]
					},
					{ key: 'load', title: 'Load average', unit: 'load', series: [] }
				]
			}
		});
		render(Page);
		const mem = (await screen.findByText('Memory used')).closest('.stat') as HTMLElement;
		expect(mem.classList.contains('bad')).toBe(true);
		expect(within(mem).getByText('Critical')).toBeTruthy();
		expect(within(mem).getByText('95%')).toBeTruthy();
		const swap = screen.getByText('Swap used').closest('.stat') as HTMLElement;
		expect(within(swap).getByText('In use')).toBeTruthy();
		const disk = screen.getByText('Root disk used').closest('.stat') as HTMLElement;
		expect(within(disk).getByText('Filling up')).toBeTruthy();
		expect(screen.getByText('Uptime').closest('.stat')?.textContent).toContain('1d 1h');
		expect(
			screen.getByText(/boson: memory used critical, swap used in use, root disk used filling up/)
		).toBeTruthy();
		expect(screen.getByText('No data in this range.')).toBeTruthy();
		expect(screen.getByText('Memory 4%')).toBeTruthy();
	});

	it('switches the range', async () => {
		serve({});
		render(Page);
		await screen.findByText('No host metrics yet');
		await userEvent.click(screen.getByRole('radio', { name: '7d' }));
		expect(api).toHaveBeenLastCalledWith(
			'GET',
			'/metrics/host?range=7d',
			undefined,
			undefined,
			expect.anything()
		);
		expect(screen.getByRole('radio', { name: '7d' }).getAttribute('aria-checked')).toBe('true');
	});
});
