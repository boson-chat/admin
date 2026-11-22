import { describe, expect, it } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import type { NetworkChannel, Role } from '$lib/types';
import NetworkChannels from './NetworkChannels.svelte';
import {
	channelAccessLabel,
	cleanChannelRoles,
	cleanNetworkChannels,
	matchChannelRole,
	founderProblem,
	networkChannelProblem,
	networkChannelsChanged,
	topicProblem
} from '$lib/channels';

describe('network channel names', () => {
	it('mirrors the BFF', () => {
		for (const ok of ['#lobby', '#Boson-Dev', '##chat', '#a[b]', '#' + 'x'.repeat(63)])
			expect(networkChannelProblem(ok)).toBe('');
		expect(networkChannelProblem('')).toMatch(/Type a channel/);
		expect(networkChannelProblem('lobby')).toMatch(/starts with #/);
		expect(networkChannelProblem('#')).toMatch(/after the #/);
		expect(networkChannelProblem('#a b')).toMatch(/spaces/);
		expect(networkChannelProblem('#a,b')).toMatch(/commas/);
		expect(networkChannelProblem('#café')).toMatch(/ASCII/);
		expect(networkChannelProblem('#' + 'x'.repeat(64))).toMatch(/64/);
		expect(networkChannelProblem('#Lobby', ['#lobby'])).toMatch(/already listed/);
	});
	it('checks topics and the founder', () => {
		expect(topicProblem('x'.repeat(300))).toBe('');
		expect(topicProblem('x'.repeat(301))).toMatch(/300/);
		expect(topicProblem('a\nb')).toMatch(/One line/);
		expect(founderProblem('')).toBe('');
		expect(founderProblem('thelion')).toBe('');
		expect(founderProblem('the lion')).toMatch(/account/);
		expect(founderProblem('#chan')).toMatch(/account/);
	});
	it('cleans and compares lists', () => {
		const a: NetworkChannel[] = [{ name: '#lobby', topic: ' Hi ', autojoin: true }];
		expect(cleanNetworkChannels(a)).toEqual([{ name: '#lobby', topic: 'Hi', autojoin: true }]);
		expect(cleanNetworkChannels([{ name: '#x', topic: '  ', autojoin: false }])).toEqual([
			{ name: '#x', autojoin: false }
		]);
		expect(networkChannelsChanged(a, [{ name: '#lobby', topic: 'Hi', autojoin: true }])).toBe(false);
		expect(networkChannelsChanged(a, [{ name: '#lobby', topic: 'Hi', autojoin: false }])).toBe(true);
	});
});

describe('NetworkChannels', () => {
	function setup(props: Record<string, unknown> = {}) {
		const state = $state({ channels: [] as NetworkChannel[], founder: '', autojoinGuests: true });
		const r = render(NetworkChannels, {
			props: {
				get channels() {
					return state.channels;
				},
				set channels(v: NetworkChannel[]) {
					state.channels = v;
				},
				get founder() {
					return state.founder;
				},
				set founder(v: string) {
					state.founder = v;
				},
				get autojoinGuests() {
					return state.autojoinGuests;
				},
				set autojoinGuests(v: boolean) {
					state.autojoinGuests = v;
				},
				...props
			}
		});
		return { state, ...r };
	}

	it('starts empty, adds (first one auto-joined), edits topics and removes', async () => {
		const { state } = setup();
		const user = userEvent.setup();
		expect(screen.getByTestId('network-channels-empty')).toBeTruthy();
		expect(screen.getByText(/founder:/).textContent).toContain('the network owner');
		expect(screen.queryByTestId('admins-only-note')).toBeNull();

		const add = screen.getByRole('textbox', { name: 'Add a channel' });
		await user.type(add, 'lobby{Enter}');
		expect(screen.getByText(/starts with #/)).toBeTruthy();
		await user.clear(add);
		await user.type(add, '#lobby{Enter}');
		await user.type(add, '#help');
		await user.click(screen.getByRole('button', { name: 'Add' }));
		await user.type(add, '#LOBBY{Enter}');
		expect(screen.getByText('#LOBBY is already listed.')).toBeTruthy();
		await user.clear(add);

		const list = screen.getByRole('list', { name: 'Network channels' });
		expect(within(list).getAllByRole('listitem')).toHaveLength(2);
		expect(state.channels).toEqual([
			{ name: '#lobby', autojoin: true },
			{ name: '#help', autojoin: false }
		]);
		expect(screen.getByText(/2 channels, 1 auto-joined/)).toBeTruthy();

		await user.type(screen.getByRole('textbox', { name: 'Topic of #lobby' }), 'Welcome');
		await user.click(screen.getByRole('checkbox', { name: 'Auto-join #help' }));
		expect(state.channels).toEqual([
			{ name: '#lobby', autojoin: true, topic: 'Welcome' },
			{ name: '#help', autojoin: true }
		]);
		expect(screen.getByText(/all auto-joined/)).toBeTruthy();

		await user.click(screen.getByRole('button', { name: 'Remove #help' }));
		expect(state.channels.map((c) => c.name)).toEqual(['#lobby']);
	});

	it('takes a founder and the guests switch, and notes admins-only creation', async () => {
		const { state } = setup({ adminsOnly: true, guestsAllowed: false });
		const user = userEvent.setup();
		expect(screen.getByTestId('admins-only-note').textContent).toContain(
			'members can only join these, role channels and channels that already exist'
		);
		const f = screen.getByRole('textbox', { name: 'Founder account' }) as HTMLInputElement;
		expect(f.placeholder).toBe('the network owner');
		await user.type(f, 'the lion');
		expect(screen.getByText('An account name, like thelion.')).toBeTruthy();
		await user.clear(f);
		await user.type(f, 'thelion');
		expect(state.founder).toBe('thelion');
		expect(screen.getByText(/founder:/).textContent).toContain('thelion');

		const sw = screen.getByRole('switch', { name: /Also auto-join guests/ }) as HTMLInputElement;
		expect(sw.checked).toBe(true);
		expect(screen.getByText(/Guest access is off/)).toBeTruthy();
		await user.click(sw);
		expect(state.autojoinGuests).toBe(false);
	});

	const role = (name: string, builtin: Role['builtin'] = null): Role =>
		({
			id: 'rol_' + name,
			name,
			slug: name.toLowerCase().replace(/ /g, '-'),
			color: null,
			position: 0,
			builtin,
			permissions: [],
			irc: { whois_line: null, vhost: null, oper: false },
			hoist: false,
			mentionable: false,
			member_count: 0
		}) as Role;
	const roles = [
		role('Owner', 'owner'),
		role('Admin', 'admin'),
		role('Chat Helper'),
		role('Member', 'member')
	];

	it('keeps a channel to roles (who can join) and says so in the summary', async () => {
		const { state } = setup({ roles });
		const user = userEvent.setup();
		const add = screen.getByRole('textbox', { name: 'Add a channel' });
		await user.type(add, '#lobby{Enter}');
		await user.type(add, '#opers{Enter}');
		const who = screen.getByRole('combobox', { name: 'Who can join #opers' });
		expect(who.textContent).toContain('Everyone');
		expect(screen.queryByTestId('restricted-summary')).toBeNull();

		await user.click(who);
		await user.click(screen.getByRole('option', { name: /^Admin/ }));
		await user.click(screen.getByRole('option', { name: /^Owner/ }));
		await user.keyboard('{Escape}');
		// most senior first, whatever order they were picked in
		expect(state.channels[1]).toEqual({ name: '#opers', autojoin: false, roles: ['Owner', 'Admin'] });
		expect(state.channels[0].roles).toBeUndefined();
		expect(who.textContent).toContain('Owner or Admin');
		expect(screen.getByTestId('restricted-summary').textContent).toBe('#opers restricted to Owner or Admin');

		// clearing them opens the channel again
		await user.click(who);
		await user.click(screen.getByRole('option', { name: /^Admin/ }));
		await user.click(screen.getByRole('option', { name: /^Owner/ }));
		await user.keyboard('{Escape}');
		expect(state.channels[1].roles).toBeUndefined();
		expect(screen.queryByTestId('restricted-summary')).toBeNull();
	});

	it('is read-only when disabled', () => {
		setup({ disabled: true });
		expect(
			(screen.getByRole('textbox', { name: 'Add a channel' }) as HTMLInputElement).matches(':disabled')
		).toBe(true);
	});
});

describe('channel roles helpers', () => {
	const roles = [
		{ name: 'Owner', slug: 'owner', builtin: 'owner' },
		{ name: 'Chat Helper', slug: 'chat-helper', builtin: null },
		{ name: 'Member', slug: 'member', builtin: 'member' }
	];
	it('cleans, matches and labels', () => {
		expect(cleanChannelRoles([' Admin ', '', 'admin', 'Owner'])).toEqual(['Admin', 'Owner']);
		expect(matchChannelRole('chat helper', roles)?.name).toBe('Chat Helper');
		expect(matchChannelRole('CHAT-HELPER', roles)?.name).toBe('Chat Helper');
		expect(matchChannelRole('nobody', roles)).toBeUndefined();
		expect(channelAccessLabel(undefined, roles)).toBe('Everyone');
		expect(channelAccessLabel(['owner'], roles)).toBe('Owner');
		expect(channelAccessLabel(['owner', 'gone'], roles)).toBe('Owner or gone');
		expect(channelAccessLabel(['owner', 'chat-helper', 'x'], roles)).toBe('3 roles');
		expect(channelAccessLabel(['owner', 'member'], roles)).toBe('All accounts');
		expect(
			cleanNetworkChannels([
				{ name: '#o', autojoin: false, roles: [' Owner', 'owner'] },
				{ name: '#a', autojoin: true, roles: [] }
			])
		).toEqual([
			{ name: '#o', autojoin: false, roles: ['Owner'] },
			{ name: '#a', autojoin: true }
		]);
	});
});
