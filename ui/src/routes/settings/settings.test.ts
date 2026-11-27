import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
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

		await user.click(screen.getByRole('radio', { name: /Only admins.*Members can only join/ }));
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

describe('network settings: network channels', () => {
	beforeEach(() => api.mockReset());

	type Body = { set?: Record<string, unknown>; unset?: string[] };
	function serveFile(keys: Record<string, unknown>, editable = true) {
		const calls: { method: string; path: string; body?: Body }[] = [];
		let etag = 'e1';
		let file = { ...keys };
		api.mockImplementation(
			async (method: string, path: string, body?: Body, hdr?: Record<string, string>) => {
				calls.push({ method, path, body });
				if (path === '/config') {
					if (method === 'PATCH') {
						expect(hdr?.['If-Match']).toBe(etag);
						file = { ...file, ...body?.set };
						for (const k of body?.unset ?? []) delete file[k];
						etag = 'e2';
					}
					return {
						enabled: true,
						etag,
						issues: [],
						keys: ['irc.channels', 'irc.channel_founder', 'irc.autojoin_guests'].map((key) => ({
							key,
							kind: key === 'irc.channels' ? 'channels' : key === 'irc.channel_founder' ? 'string' : 'bool',
							set: key in file,
							value: file[key],
							editable
						}))
					};
				}
				return {
					settings: method === 'PATCH' ? { ...base, ...(body as object) } : { ...base, guest_access: true },
					config: { managed: true, sources: {} }
				};
			}
		);
		return calls;
	}

	it('edits the list with the rest of the page and saves it to the file', async () => {
		const calls = serveFile({});
		const user = userEvent.setup();
		render(Page);
		expect(await screen.findByTestId('network-channels-empty')).toBeTruthy();
		await user.type(screen.getByRole('textbox', { name: 'Add a channel' }), '#lobby{Enter}');
		await user.type(screen.getByRole('textbox', { name: 'Add a channel' }), '#help{Enter}');
		await user.type(screen.getByRole('textbox', { name: 'Topic of #lobby' }), 'Welcome to Boson');
		await user.click(screen.getByRole('radio', { name: /Only admins/ }));
		expect(screen.getByTestId('admins-only-note')).toBeTruthy();
		expect(screen.getByText('2 unsaved changes')).toBeTruthy();
		// Enter in the add box added channels; nothing was saved yet.
		expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(0);

		await user.click(saveButton());
		const patches = calls.filter((c) => c.method === 'PATCH');
		expect(patches[0]).toEqual({
			method: 'PATCH',
			path: '/config',
			body: {
				set: {
					'irc.channels': [
						{ name: '#lobby', topic: 'Welcome to Boson', autojoin: true },
						{ name: '#help', autojoin: false }
					]
				},
				unset: []
			}
		});
		expect(patches[1]).toEqual({ method: 'PATCH', path: '/settings', body: { channel_creation: 'admins' } });
		await waitFor(() => expect(screen.queryByText(/unsaved change/)).toBeNull());
	});

	it('shows the file’s channels, founder and guests switch, and resets the founder', async () => {
		const calls = serveFile({
			'irc.channels': [{ name: '#lobby', autojoin: true }],
			'irc.channel_founder': 'thelion',
			'irc.autojoin_guests': false
		});
		const user = userEvent.setup();
		render(Page);
		expect(await screen.findByRole('checkbox', { name: 'Auto-join #lobby' })).toHaveProperty('checked', true);
		expect((screen.getByRole('switch', { name: /Also auto-join guests/ }) as HTMLInputElement).checked).toBe(
			false
		);
		const f = screen.getByRole('textbox', { name: 'Founder account' });
		expect((f as HTMLInputElement).value).toBe('thelion');
		await user.clear(f);
		await user.click(screen.getByRole('switch', { name: /Also auto-join guests/ }));
		await user.click(saveButton());
		expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({
			set: { 'irc.autojoin_guests': true },
			unset: ['irc.channel_founder']
		});
		// Discard brings back the saved values.
		await user.click(screen.getByRole('button', { name: 'Remove #lobby' }));
		await user.click(screen.getByRole('button', { name: 'Discard' }));
		expect(screen.getByRole('checkbox', { name: 'Auto-join #lobby' })).toBeTruthy();
	});

	it('is read-only without settings.manage', async () => {
		serveFile({}, false);
		render(Page);
		expect(await screen.findByText(/saving needs/)).toBeTruthy();
		expect(
			(screen.getByRole('textbox', { name: 'Add a channel' }) as HTMLInputElement).matches(':disabled')
		).toBe(true);
	});
});

describe('network settings: guests first', () => {
	beforeEach(() => api.mockReset());
	const firstSwitch = () => screen.getByRole('switch', { name: /Guests first/ }) as HTMLInputElement;

	it('turns guests first on with a landing channel and sends only those fields', async () => {
		serve({ ...base, guest_access: true, guest_first: false, guest_first_channel: '' });
		const user = userEvent.setup();
		render(Page);
		const sw = (await screen.findByRole('switch', { name: /Guests first/ })) as HTMLInputElement;
		expect(sw.checked).toBe(false);
		expect(sw.hasAttribute('disabled')).toBe(false);
		expect(screen.queryByRole('combobox', { name: 'Land on' })).toBeNull();

		await user.click(sw);
		const box = screen.getByRole('combobox', { name: 'Land on' });
		await user.type(box, '#IRCForever');
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', {
			guest_first: true,
			guest_first_channel: '#IRCForever'
		});
		expect(firstSwitch().checked).toBe(true);
	});

	it('blocks saving a landing channel that is not a channel name', async () => {
		serve({ ...base, guest_access: true, guest_first: true, guest_first_channel: '' });
		const user = userEvent.setup();
		render(Page);
		await user.type(await screen.findByRole('combobox', { name: 'Land on' }), 'lobby');
		expect(screen.getByText(/starts with #/)).toBeTruthy();
		expect(screen.getByText('Fix the guests first channel to save')).toBeTruthy();
	});

	it('turns off with guest access, and saving sends both', async () => {
		serve({ ...base, guest_access: true, guest_first: true, guest_first_channel: '#lobby' });
		const user = userEvent.setup();
		render(Page);
		expect((await screen.findByRole('switch', { name: /Guests first/ })).hasAttribute('disabled')).toBe(
			false
		);
		await user.click(screen.getByRole('switch', { name: /Guest access/ }));
		expect(firstSwitch().hasAttribute('disabled')).toBe(true);
		expect(screen.getByText(/turns guests first off too/)).toBeTruthy();
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', { guest_access: false, guest_first: false });
	});

	it('disables the toggle on older auth and never sends it', async () => {
		serve({ ...base, guest_access: true });
		const user = userEvent.setup();
		render(Page);
		expect(await screen.findByText(/Until then the web client opens on the sign-in screen/)).toBeTruthy();
		expect(firstSwitch().hasAttribute('disabled')).toBe(true);
		await user.click(screen.getByRole('switch', { name: /Guest access/ }));
		await user.click(saveButton());
		expect(api).toHaveBeenLastCalledWith('PATCH', '/settings', { guest_access: false });
	});
});
