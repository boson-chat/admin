import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';

const api = vi.hoisted(() => vi.fn());
const perms = vi.hoisted(() => ({ list: ['admin.panel', 'roles.manage', 'settings.manage'] }));
vi.mock('$lib/api', () => ({ api, isUnsupported: () => false }));
vi.mock('$app/navigation', () => ({ beforeNavigate: () => {}, goto: vi.fn() }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://x/channels') } }));
vi.mock('$lib/session.svelte', () => ({
	session: {
		info: { features: { config_file: true }, permissions: perms.list },
		can: (p: string) => perms.list.includes(p)
	}
}));

import Page from './+page.svelte';

const overview = {
	roles: [
		{
			id: 'rol_mods',
			name: 'Mods',
			color: '#3498db',
			position: 500,
			channels: [
				{ channel: '#mods', autojoin: true },
				{ channel: '#staff', autojoin: true }
			]
		},
		{
			id: 'everyone',
			name: 'Everyone',
			color: null,
			position: 0,
			everyone: true,
			channels: [{ channel: '#lobby', autojoin: true }]
		}
	],
	channels: [
		{
			channel: '#lobby',
			private: false,
			roles: [{ id: 'everyone', name: 'Everyone', color: null, autojoin: true }]
		},
		{
			channel: '#mods',
			private: false,
			roles: [{ id: 'rol_mods', name: 'Mods', color: '#3498db', autojoin: true }]
		},
		{
			channel: '#staff',
			private: true,
			roles: [{ id: 'rol_mods', name: 'Mods', color: '#3498db', autojoin: true }]
		}
	]
};

let calls: { method: string; path: string; body?: unknown }[] = [];
function serve(autojoin: boolean, rejoin = 'login') {
	api.mockImplementation(async (method: string, path: string, body?: unknown) => {
		calls.push({ method, path, body });
		if (path === '/channels') return overview;
		if (path === '/config')
			return {
				enabled: true,
				etag: method === 'PATCH' ? 'e2' : 'e1',
				issues: [],
				keys: [
					{
						key: 'roles.channels.autojoin',
						kind: 'bool',
						set: true,
						value: method === 'PATCH' ? true : autojoin,
						editable: perms.list.includes('settings.manage')
					},
					{
						key: 'roles.channels.rejoin',
						kind: 'enum',
						enum: ['login', 'once'],
						set: true,
						value: method === 'PATCH' ? 'once' : rejoin,
						editable: perms.list.includes('settings.manage')
					}
				]
			};
		return {};
	});
}

describe('channel access page', () => {
	beforeEach(() => {
		api.mockReset();
		calls = [];
		perms.list = ['admin.panel', 'roles.manage', 'settings.manage'];
	});

	it('previews who is joined where, and marks channels private', async () => {
		serve(true);
		render(Page);
		expect(await screen.findByText('Members of Mods will be joined to #mods and #staff.')).toBeTruthy();
		expect(screen.getByText('Everyone will be joined to #lobby.')).toBeTruthy();

		const user = userEvent.setup();
		const list = screen.getByRole('list', { name: 'Channels' });
		expect(within(list).getAllByText('Private')).toHaveLength(1 + 3); // one badge + a switch label per row
		await user.click(screen.getByRole('switch', { name: '#mods is private' }));
		const input = screen.getByRole('textbox', { name: /Make a channel private/ });
		await user.type(input, '#staff{Enter}');
		expect(screen.getByText('#staff is already private.')).toBeTruthy();
		await user.clear(input);
		await user.type(input, '#Secret{Enter}');
		expect(screen.getByText('No role lists it, so only IRC operators can join.')).toBeTruthy();
		expect(screen.getByText('2 unsaved changes')).toBeTruthy();
		await user.click(screen.getByRole('button', { name: 'Save channel access' }));
		const put = calls.find((c) => c.method === 'PUT');
		expect(put).toEqual({
			method: 'PUT',
			path: '/channel-access',
			body: {
				channels: [
					{ channel: '#lobby', private: false },
					{ channel: '#mods', private: true },
					{ channel: '#secret', private: true },
					{ channel: '#staff', private: true }
				]
			}
		});
	});

	it('saves the auto-join switches to the config file', async () => {
		serve(false);
		render(Page);
		const sw = await screen.findByRole('switch', { name: /Auto-join members to their roles’ channels/ });
		expect(
			await screen.findByText('Auto-join is off, so this is what would happen with it on.')
		).toBeTruthy();
		const user = userEvent.setup();
		await user.click(sw);
		await user.click(screen.getByRole('radio', { name: /Leave them out/ }));
		expect(screen.getByRole('radio', { name: /Join them again at next login/ })).toBeTruthy();
		await user.click(screen.getByRole('button', { name: 'Save auto-join' }));
		const patch = calls.find((c) => c.method === 'PATCH');
		expect(patch?.body).toEqual({
			set: { 'roles.channels.autojoin': true, 'roles.channels.rejoin': 'once' }
		});
	});

	it('needs settings.manage for the switches and roles.manage for access', async () => {
		perms.list = ['admin.panel'];
		serve(true);
		render(Page);
		expect(await screen.findByText(/saving needs/)).toBeTruthy();
		expect(screen.getByRole('switch', { name: /Auto-join members/ }).matches(':disabled')).toBe(true);
		expect(screen.queryByRole('button', { name: 'Save channel access' })).toBeNull();
		expect(screen.queryByRole('textbox', { name: /Make a channel private/ })).toBeNull();
		expect((screen.getByRole('switch', { name: '#staff is private' }) as HTMLInputElement).disabled).toBe(
			true
		);
	});
});
