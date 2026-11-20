import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import type { ConfigKey, ConfigView } from '$lib/types';

const api = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => ({ api, isUnsupported: () => false }));

import Page from './+page.svelte';

const key = (k: Partial<ConfigKey> & { key: string; kind: ConfigKey['kind'] }): ConfigKey => ({
	perm: 'directory.manage',
	services: ['admin'],
	set: false,
	editable: true,
	...k
});

function view(over: Partial<ConfigView> = {}): ConfigView {
	return {
		enabled: true,
		path: '/config/boson.yaml',
		etag: '"v1"',
		exists: true,
		modified_at: '2026-11-14T12:00:00Z',
		saved_at: '2026-11-14T12:00:00Z',
		saved_by: 'root',
		issues: [],
		keys: [
			key({
				key: 'admin.directory_registration',
				kind: 'bool',
				default: true,
				env: 'DIRECTORY_REGISTRATION'
			}),
			key({
				key: 'admin.directory_hostname',
				kind: 'string',
				set: true,
				value: 'irc.file.example',
				line: 4,
				default: 'irc.env.example'
			}),
			key({
				key: 'admin.tls_check_hosts',
				kind: 'list',
				perm: 'services.manage',
				default: ['irc.env.example:6697'],
				env: 'TLS_CHECK_HOSTS'
			}),
			key({ key: 'voice.rooms_strict', kind: 'bool', perm: 'settings.manage', services: ['quicvoiced'] })
		],
		...over
	};
}

let current: ConfigView;
function serve(v: ConfigView) {
	current = v;
	api.mockImplementation(
		async (method: string, path: string, body?: { set?: Record<string, unknown>; unset?: string[] }) => {
			if (path === '/config/status')
				return {
					services: [
						{ name: 'admin', state: 'applied', restart_required: [], sections: ['admin'] },
						{
							name: 'ergo',
							state: 'restart_required',
							restart_required: ['irc.persistent_history'],
							sections: ['irc']
						}
					]
				};
			if (path === '/config/raw')
				return {
					path: current.path,
					exists: true,
					etag: '"v1"',
					content: 'version: 1\nadmin:\n  x: [\n',
					error: current.error
				};
			if (method === 'PATCH') {
				current = {
					...current,
					etag: '"v2"',
					keys: current.keys.map((k) => {
						if (body?.unset?.includes(k.key)) return { ...k, set: false, value: undefined };
						if (body?.set && k.key in body.set)
							return { ...k, set: true, value: body.set[k.key] as ConfigKey['value'] };
						return k;
					})
				};
				return current;
			}
			return current;
		}
	);
}

describe('stack config page', () => {
	beforeEach(() => api.mockReset());

	it('shows where values come from and saves only changed keys with If-Match', async () => {
		serve(view());
		const user = userEvent.setup();
		render(Page);
		expect(await screen.findByText('/config/boson.yaml')).toBeTruthy();
		expect(screen.getByText('In the file · line 4')).toBeTruthy();
		expect(screen.getByText(/From TLS_CHECK_HOSTS in the environment: irc.env.example:6697/)).toBeTruthy();

		const hosts = screen.getByRole('textbox', { name: 'Certificates to watch' });
		await user.clear(hosts);
		await user.type(hosts, 'irc.example.org:6697{Enter}voice.example.org:443');
		expect(screen.getByText('One unsaved change')).toBeTruthy();
		const admin = screen.getByRole('heading', { name: 'Admin panel' }).closest('section')!;
		await user.click(within(admin as HTMLElement).getByRole('button', { name: 'Save changes' }));
		expect(api).toHaveBeenCalledWith(
			'PATCH',
			'/config',
			{ set: { 'admin.tls_check_hosts': ['irc.example.org:6697', 'voice.example.org:443'] } },
			{ 'If-Match': '"v1"' }
		);
	});

	it('resets a key to its default by removing it', async () => {
		serve(view());
		const user = userEvent.setup();
		render(Page);
		await user.click(await screen.findByRole('button', { name: 'Reset to default' }));
		expect(api).toHaveBeenCalledWith(
			'PATCH',
			'/config',
			{ unset: ['admin.directory_hostname'] },
			{ 'If-Match': '"v1"' }
		);
		expect(await screen.findByText(/From the environment: irc.env.example/)).toBeTruthy();
	});

	it('shows a parse error with its line and offers the raw file', async () => {
		serve(view({ error: { message: 'did not find expected node content', line: 3 } }));
		render(Page);
		const alert = await screen.findByText(/The file has an error on line 3/);
		expect(alert.closest('[role="alert"]')?.textContent).toMatch(/keeps its last good settings/);
		// The file view opens by itself and marks the line.
		expect(await screen.findByLabelText('Contents of /config/boson.yaml')).toBeTruthy();
	});

	it('lists services that need a restart', async () => {
		serve(view());
		render(Page);
		expect(await screen.findByText('Needs a restart')).toBeTruthy();
		expect(screen.getByText('irc.persistent_history')).toBeTruthy();
	});

	it('locks keys the account can’t change', async () => {
		const v = view();
		v.keys = v.keys.map((k) => (k.perm === 'services.manage' ? { ...k, editable: false } : k));
		serve(v);
		render(Page);
		const hosts = (await screen.findByRole('textbox', {
			name: 'Certificates to watch'
		})) as HTMLTextAreaElement;
		expect(hosts.disabled).toBe(true);
		expect(screen.getByText(/Changing it needs/)).toBeTruthy();
	});

	it('sets a voice room’s bitrate cap', async () => {
		const v = view();
		v.keys = [
			...v.keys,
			key({
				key: 'voice.rooms',
				kind: 'rooms',
				perm: 'settings.manage',
				services: ['quicvoiced'],
				set: true,
				line: 6,
				value: [
					{ id: 'general', name: 'General' },
					{ id: 'music', name: 'Music', max_kbps: 128 }
				]
			}),
			key({
				key: 'voice.max_kbps',
				kind: 'int',
				min: 0,
				max: 510,
				perm: 'settings.manage',
				services: ['quicvoiced']
			})
		];
		serve(v);
		const user = userEvent.setup();
		render(Page);
		const general = await screen.findByRole('combobox', { name: 'Room 1 voice quality cap' });
		expect(general.textContent).toMatch(/No room cap/);
		expect(screen.getByRole('combobox', { name: 'Room 2 voice quality cap' }).textContent).toMatch(
			/128 kbps/
		);
		expect(screen.getByText('Voice quality cap')).toBeTruthy();

		await user.click(general);
		await user.click(await screen.findByRole('option', { name: '64 kbps' }));
		await user.click(screen.getByRole('combobox', { name: 'Room 2 voice quality cap' }));
		await user.click(await screen.findByRole('option', { name: 'No room cap' }));
		const voice = screen.getByRole('heading', { name: 'Voice rooms' }).closest('section')!;
		await user.click(within(voice as HTMLElement).getByRole('button', { name: 'Save changes' }));
		expect(api).toHaveBeenCalledWith(
			'PATCH',
			'/config',
			{
				set: {
					'voice.rooms': [
						{ id: 'general', name: 'General', max_kbps: 64 },
						{ id: 'music', name: 'Music' }
					]
				}
			},
			{ 'If-Match': '"v1"' }
		);
	});

	it('explains the file isn’t set up', async () => {
		serve({ ...view(), enabled: false });
		render(Page);
		expect(await screen.findByText('There’s no config file yet')).toBeTruthy();
	});
});
