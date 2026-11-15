import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';

const api = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => ({ api, isUnsupported: (e: { code?: string }) => e?.code === 'unsupported' }));
vi.mock('$app/navigation', () => ({ beforeNavigate: () => {}, goto: vi.fn() }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://x/roles/rol_mods') } }));

import RoleChannels from './RoleChannels.svelte';

type Body = { channels: { channel: string; autojoin: boolean }[] };
let puts: Body[] = [];
let autojoinOn = true;

function serve(channels: Body['channels']) {
	api.mockImplementation(async (method: string, path: string, body?: Body) => {
		if (path === '/config')
			return {
				enabled: true,
				issues: [],
				keys: [{ key: 'roles.channels.autojoin', kind: 'bool', set: true, value: autojoinOn, editable: true }]
			};
		if (path === '/channel-access') return { channels: [{ channel: '#staff', private: true }] };
		if (method === 'PUT') {
			puts.push(body!);
			return { channels: body!.channels };
		}
		return { channels };
	});
}

describe('a role’s channels', () => {
	beforeEach(() => {
		api.mockReset();
		puts = [];
		autojoinOn = true;
	});

	it('lists channels with their access, previews and saves the whole list', async () => {
		serve([{ channel: '#staff', autojoin: true }]);
		render(RoleChannels, { roleId: 'rol_mods', roleName: 'Mods' });
		const list = await screen.findByRole('list', { name: 'Channels' });
		expect(within(list).getByText('#staff')).toBeTruthy();
		expect(within(list).getByText('Private')).toBeTruthy();
		expect((await screen.findByTestId('channel-preview')).textContent).toContain(
			'Members of Mods will be joined to #staff.'
		);

		const user = userEvent.setup();
		const input = screen.getByRole('textbox', { name: /Add a channel/ });
		await user.type(input, 'mods{Enter}');
		expect(screen.getByText('A channel name starts with #, like #staff.')).toBeTruthy();
		expect(input.getAttribute('aria-invalid')).toBe('true');
		await user.clear(input);
		await user.type(input, '#STAFF{Enter}');
		expect(screen.getByText('#staff is already listed.')).toBeTruthy();
		await user.clear(input);
		await user.type(input, '#Mods{Enter}');
		expect(within(list).getByText('#mods')).toBeTruthy();
		expect(within(list).getByText('Public')).toBeTruthy();
		expect(screen.getByTestId('channel-preview').textContent).toContain('joined to #mods and #staff');

		// untick auto-join on #staff: it drops out of the preview
		const boxes = within(list).getAllByRole('checkbox', { name: 'Auto-join' });
		await user.click(boxes[1]);
		expect(screen.getByTestId('channel-preview').textContent).toContain('joined to #mods.');

		await user.click(screen.getByRole('button', { name: 'Save channels' }));
		expect(puts).toEqual([
			{
				channels: [
					{ channel: '#mods', autojoin: true },
					{ channel: '#staff', autojoin: false }
				]
			}
		]);
		expect(api).toHaveBeenCalledWith('PUT', '/roles/rol_mods/channels', puts[0]);
		expect(await screen.findByText('All changes saved')).toBeTruthy();
	});

	it('removes channels and says when auto-join is off for the network', async () => {
		autojoinOn = false;
		serve([
			{ channel: '#lobby', autojoin: true },
			{ channel: '#rules', autojoin: false }
		]);
		render(RoleChannels, { roleId: 'everyone', roleName: 'Everyone', everyone: true });
		const preview = await screen.findByTestId('channel-preview');
		await vi.waitFor(() => expect(preview.textContent).toContain('Auto-join is off for the network'));
		expect(preview.textContent).toContain('With auto-join on, everyone would be joined to #lobby.');
		const user = userEvent.setup();
		await user.click(screen.getByRole('button', { name: 'Remove #lobby' }));
		await user.click(screen.getByRole('button', { name: 'Save channels' }));
		expect(api).toHaveBeenCalledWith('PUT', '/roles/everyone/channels', {
			channels: [{ channel: '#rules', autojoin: false }]
		});
	});

	it('is read-only when the role is locked', async () => {
		serve([{ channel: '#staff', autojoin: true }]);
		render(RoleChannels, {
			roleId: 'rol_owner',
			roleName: 'Owner',
			lock: 'You can only change roles below your own.'
		});
		await screen.findByRole('list', { name: 'Channels' });
		expect(screen.queryByRole('textbox')).toBeNull();
		expect(screen.queryByRole('button', { name: /Remove/ })).toBeNull();
		expect((screen.getByRole('checkbox', { name: 'Auto-join' }) as HTMLInputElement).disabled).toBe(true);
		expect(screen.getByText('You can only change roles below your own.')).toBeTruthy();
	});

	it('explains an auth service without role channels', async () => {
		api.mockImplementation(async (_m: string, path: string) => {
			if (path === '/config') return { enabled: true, issues: [], keys: [] };
			throw Object.assign(new Error('Role channels need a newer auth service (auth ≥ v1.16.0)'), {
				code: 'unsupported'
			});
		});
		render(RoleChannels, { roleId: 'rol_mods', roleName: 'Mods' });
		expect(await screen.findByText('Role channels need a newer auth service')).toBeTruthy();
	});
});
