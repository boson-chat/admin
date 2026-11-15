import { describe, expect, it } from 'vitest';
import { channelProblem, channelsChanged, foldChannel, listChannels, previewLine } from './channels';

describe('channel names', () => {
	it('folds and accepts IRC channel names', () => {
		expect(foldChannel('  #Staff ')).toBe('#staff');
		for (const ok of ['#staff', '#Mods', '#a', '#ünï', '#' + 'x'.repeat(63)])
			expect(channelProblem(ok)).toBe('');
	});
	it('explains bad ones', () => {
		expect(channelProblem('')).toMatch(/Type a channel/);
		expect(channelProblem('staff')).toMatch(/starts with #/);
		expect(channelProblem('#')).toMatch(/after the #/);
		expect(channelProblem('#a b')).toMatch(/No spaces/);
		expect(channelProblem('#a,b')).toMatch(/commas/);
		expect(channelProblem('#a\u0007')).toMatch(/control/);
		expect(channelProblem('#' + 'x'.repeat(64))).toMatch(/64/);
		// bytes, not characters, like Ergo
		expect(channelProblem('#' + 'é'.repeat(32))).toMatch(/64/);
	});
});

describe('preview', () => {
	const ch = [
		{ channel: '#staff', autojoin: true },
		{ channel: '#mods', autojoin: true },
		{ channel: '#logs', autojoin: false }
	];
	it('names the auto-join channels only', () => {
		expect(previewLine('Mods', ch)).toBe('Members of Mods will be joined to #staff and #mods.');
		expect(previewLine('Member', ch.slice(0, 1), true)).toBe('Everyone will be joined to #staff.');
		expect(listChannels(['#a', '#b', '#c'])).toBe('#a, #b and #c');
	});
	it('says what happens with auto-join off or nothing to join', () => {
		expect(previewLine('Mods', ch, false, false)).toBe(
			'With auto-join on, members of Mods would be joined to #staff and #mods.'
		);
		expect(previewLine('Mods', [ch[2]])).toBe('Members of Mods aren’t joined anywhere automatically.');
		expect(previewLine('Mods', [])).toBe('Mods has no channels yet.');
		expect(previewLine('Member', [], true)).toBe('No channels for everyone yet.');
	});
	it('compares lists regardless of order', () => {
		expect(channelsChanged(ch, [...ch].reverse())).toBe(false);
		expect(channelsChanged(ch, [{ ...ch[0], autojoin: false }, ch[1], ch[2]])).toBe(true);
		expect(channelsChanged(ch, ch.slice(1))).toBe(true);
	});
});
