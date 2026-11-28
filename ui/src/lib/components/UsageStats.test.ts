import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import type { TelemetryStatus } from '$lib/telemetry';

const api = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => ({ api }));

import UsageStats from './UsageStats.svelte';

const base: TelemetryStatus = {
	enabled: true,
	locked: false,
	file_on: true,
	config_file: true,
	endpoint: 'https://telemetry.boson.chat/v1/report',
	last: { at: '2026-11-27T10:00:00Z', reason: 'daily', ok: false, status: 500 },
	last_sent_at: '2026-11-27T10:00:00Z',
	next_at: '2026-11-28T10:20:00Z',
	next_reason: 'daily'
};

function serve(s: TelemetryStatus) {
	api.mockImplementation(async (method: string, path: string, body?: { enabled: boolean }) => {
		if (path === '/telemetry/preview')
			return {
				schema: 1,
				install_id: '3f2a9c0e5b7d41e8a6c2f90b1d4e7a53',
				host_size: { cpu_cores: '3-4', ram_gb: '4-8', disk_gb: null, arch: 'amd64' }
			};
		if (method === 'PUT') return { ...s, enabled: !!body?.enabled, file_on: !!body?.enabled };
		return s;
	});
}

beforeEach(() => {
	api.mockReset();
	// jsdom has no <dialog> methods
	HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
		this.open = true;
	};
	HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
		this.open = false;
	};
});

describe('usage & health stats card', () => {
	it('leads with the why, shows the last result and turns off', async () => {
		serve(base);
		const user = userEvent.setup();
		render(UsageStats);
		expect(screen.getByText(/We collect a small amount of anonymous usage and health data/)).toBeTruthy();
		const sw = await screen.findByRole('switch', { name: /Send usage & health stats/ });
		await waitFor(() => expect((sw as HTMLInputElement).checked).toBe(true));
		expect(screen.getByTestId('usage-result').textContent).toContain('Refused (HTTP 500)');
		expect(screen.getByRole('link', { name: /What’s collected/ }).getAttribute('href')).toBe(
			'https://boson.chat/telemetry'
		);
		await user.click(sw);
		expect(api).toHaveBeenCalledWith('PUT', '/telemetry', { enabled: false });
		await waitFor(() => expect((sw as HTMLInputElement).checked).toBe(false));
	});

	it('is off and locked with TELEMETRY=off in .env', async () => {
		serve({ ...base, enabled: false, locked: true, next_at: undefined });
		render(UsageStats);
		const sw = (await screen.findByRole('switch', { name: /Send usage/ })) as HTMLInputElement;
		await waitFor(() => expect(sw.disabled).toBe(true));
		expect(sw.checked).toBe(false);
		expect(screen.getByText(/in the stack’s/).textContent).toContain('.env');
		expect(screen.getByText('Not sending')).toBeTruthy();
	});

	it('shows the next report as JSON', async () => {
		serve(base);
		const user = userEvent.setup();
		render(UsageStats);
		await user.click(await screen.findByRole('button', { name: 'See exactly what’s sent' }));
		const pre = await screen.findByTestId('usage-preview');
		expect(JSON.parse(pre.textContent ?? '')).toEqual({
			schema: 1,
			install_id: '3f2a9c0e5b7d41e8a6c2f90b1d4e7a53',
			host_size: { cpu_cores: '3-4', ram_gb: '4-8', disk_gb: null, arch: 'amd64' }
		});
		expect(screen.getByText(/is a range too, never the exact figure/)).toBeTruthy();
		expect(api).toHaveBeenCalledWith('GET', '/telemetry/preview');
	});
});
