import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import type { NetworkSettings } from '$lib/types';

const api = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => ({ api, isUnsupported: () => false }));

import Page from './+page.svelte';

const base: NetworkSettings = {
	registration: 'invite',
	require_email_verification: false,
	channel_creation: 'registered',
	voice_channel_creation: 'founders'
};

function serve(settings: NetworkSettings) {
	api.mockImplementation(async (method: string, _path: string, body?: Partial<NetworkSettings>) => ({
		settings: method === 'PATCH' ? { ...settings, ...body } : settings
	}));
}

const expiryBox = () => screen.getByRole('combobox', { name: 'Expire inactive channels' });
const saveButton = () => screen.getByRole('button', { name: 'Save changes' });

describe('network settings: channel registration', () => {
	beforeEach(() => api.mockReset());

	it('shows a stored non-preset expiry as Custom and saves only what changed', async () => {
		serve({ ...base, channel_auto_register: true, channel_expiry_days: 45 });
		const user = userEvent.setup();
		render(Page);
		expect(
			(await screen.findByRole('switch', { name: /Register new channels/ })).hasAttribute('disabled')
		).toBe(false);
		expect(expiryBox().textContent).toContain('Custom');
		const days = screen.getByRole('spinbutton', { name: /many days/ }) as HTMLInputElement;
		expect(days.value).toBe('45');
		expect(screen.queryByText(/Requires auth 1.7/)).toBeNull();

		await user.click(screen.getByRole('switch', { name: /Register new channels/ }));
		await user.click(expiryBox());
		await user.click(screen.getByRole('option', { name: /90 days/ }));
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', {
			channel_auto_register: false,
			channel_expiry_days: 90
		});
	});

	it('flags an out-of-range custom expiry and blocks saving', async () => {
		serve({ ...base, channel_auto_register: true, channel_expiry_days: 0 });
		const user = userEvent.setup();
		render(Page);
		await user.click(await screen.findByRole('combobox', { name: 'Expire inactive channels' }));
		await user.click(screen.getByRole('option', { name: /Custom/ }));
		const days = screen.getByRole('spinbutton', { name: /many days/ });
		await user.clear(days);
		await user.type(days, '4000');
		expect(screen.getByRole('alert').textContent).toMatch(/1 to 3,650 days/);
		expect(days.getAttribute('aria-invalid')).toBe('true');
		expect(saveButton().hasAttribute('disabled')).toBe(true);

		await user.clear(days);
		await user.type(days, '14');
		expect(screen.queryByRole('alert')).toBeNull();
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', { channel_expiry_days: 14 });
	});

	it('disables the group on auth older than 1.7 and never sends the fields', async () => {
		serve(base);
		const user = userEvent.setup();
		render(Page);
		expect(await screen.findByText(/Requires auth 1.7 or newer/)).toBeTruthy();
		expect(screen.getByRole('switch', { name: /Register new channels/ }).hasAttribute('disabled')).toBe(true);
		expect(expiryBox().hasAttribute('disabled')).toBe(true);

		await user.click(screen.getByRole('radio', { name: /Admins only.*create channels/ }));
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', { channel_creation: 'admins' });
	});
});
