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

describe('network settings: guest access', () => {
	beforeEach(() => api.mockReset());
	const guestSwitch = () => screen.getByRole('switch', { name: /Guest access/ });

	it('turns guest access off and sends only guest_access', async () => {
		serve({ ...base, channel_auto_register: true, channel_expiry_days: 0, guest_access: true });
		const user = userEvent.setup();
		render(Page);
		const sw = (await screen.findByRole('switch', { name: /Guest access/ })) as HTMLInputElement;
		expect(sw.checked).toBe(true);
		expect(sw.hasAttribute('disabled')).toBe(false);
		expect(screen.queryByText(/Requires auth 1.8/)).toBeNull();

		await user.click(sw);
		expect(screen.getByText(/refuse connections that don’t sign in/)).toBeTruthy();
		expect(screen.getByText('One unsaved change')).toBeTruthy();
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', { guest_access: false });
		expect((guestSwitch() as HTMLInputElement).checked).toBe(false);
	});

	it('shows a stored false and turns guests back on', async () => {
		serve({ ...base, guest_access: false });
		const user = userEvent.setup();
		render(Page);
		const sw = (await screen.findByRole('switch', { name: /Guest access/ })) as HTMLInputElement;
		expect(sw.checked).toBe(false);
		expect(screen.getByText(/IRC refuses connections that don’t sign in/)).toBeTruthy();
		await user.click(sw);
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', { guest_access: true });
	});

	it('disables the toggle on auth older than 1.8 and never sends it', async () => {
		serve({ ...base, channel_auto_register: true, channel_expiry_days: 0 });
		const user = userEvent.setup();
		render(Page);
		expect(await screen.findByText(/Requires auth 1.8 or newer/)).toBeTruthy();
		expect(guestSwitch().hasAttribute('disabled')).toBe(true);
		expect((guestSwitch() as HTMLInputElement).checked).toBe(true);

		await user.click(screen.getByRole('switch', { name: /Require a verified email/ }));
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', { require_email_verification: true });
	});
});

describe('network settings: stack config file', () => {
	beforeEach(() => api.mockReset());

	it('says the file manages them and resets a key set there', async () => {
		api.mockImplementation(async (method: string, path: string) => {
			if (path === '/config') return {};
			return {
				settings: { ...base, guest_access: true, channel_auto_register: true, channel_expiry_days: 0 },
				config: { managed: true, sources: { registration: 'file', guest_access: 'default' } }
			};
		});
		const user = userEvent.setup();
		render(Page);
		expect(await screen.findByText(/Saved to the stack config file/)).toBeTruthy();
		await user.click(screen.getByRole('button', { name: 'Reset Registration to its default' }));
		expect(api).toHaveBeenCalledWith('PATCH', '/config', { unset: ['auth.registration'] });
	});
});

describe('network settings: guest voice', () => {
	beforeEach(() => api.mockReset());
	const voiceSwitch = () => screen.getByRole('switch', { name: /Guests can join voice/ }) as HTMLInputElement;

	it('turns guest voice on and sends only guest_voice', async () => {
		serve({ ...base, guest_access: true, guest_voice: false });
		const user = userEvent.setup();
		render(Page);
		const sw = (await screen.findByRole('switch', {
			name: /Guests can join voice/
		})) as HTMLInputElement;
		expect(sw.checked).toBe(false);
		expect(sw.hasAttribute('disabled')).toBe(false);
		expect(screen.queryByText(/Requires auth 1.18/)).toBeNull();

		await user.click(sw);
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', { guest_voice: true });
		expect(voiceSwitch().checked).toBe(true);
	});

	it('is disabled while guest access is off, including after turning it off', async () => {
		serve({ ...base, guest_access: true, guest_voice: true });
		const user = userEvent.setup();
		render(Page);
		const sw = (await screen.findByRole('switch', {
			name: /Guests can join voice/
		})) as HTMLInputElement;
		expect(sw.hasAttribute('disabled')).toBe(false);
		await user.click(screen.getByRole('switch', { name: /Guest access/ }));
		expect(voiceSwitch().hasAttribute('disabled')).toBe(true);
		expect(screen.getByText(/Turn on guest access first/)).toBeTruthy();
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', { guest_access: false });
	});

	it('disables the toggle on auth older than 1.18 and never sends it', async () => {
		serve({ ...base, guest_access: true });
		const user = userEvent.setup();
		render(Page);
		expect(await screen.findByText(/Requires auth 1.18 or newer/)).toBeTruthy();
		expect(voiceSwitch().hasAttribute('disabled')).toBe(true);
		expect(voiceSwitch().checked).toBe(false);

		await user.click(screen.getByRole('switch', { name: /Guest access/ }));
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', { guest_access: false });
	});
});
