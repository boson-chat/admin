import type { RoleChannel } from './types';

/** Config keys of the auto-join switches. */
export const AUTOJOIN_KEY = 'roles.channels.autojoin';
export const REJOIN_KEY = 'roles.channels.rejoin';
export type Rejoin = 'login' | 'once';

/** Auth's role ID for the channels everyone gets (the Member role). */
export const EVERYONE = 'everyone';

/** Ergo's default channel length limit, in bytes with the #. */
export const MAX_CHANNEL = 64;

/** A channel name as auth stores it: trimmed and casefolded. */
export function foldChannel(s: string): string {
	return s.trim().toLowerCase();
}

/** Why `s` isn't a channel name the server takes, or '' (mirrors the BFF). */
export function channelProblem(s: string): string {
	const c = foldChannel(s);
	if (!c) return 'Type a channel name, like #staff.';
	if (!c.startsWith('#')) return 'A channel name starts with #, like #staff.';
	if (c.length === 1) return 'Add a name after the #.';
	if (new TextEncoder().encode(c).length > MAX_CHANNEL) return 'At most 64 characters.';
	// eslint-disable-next-line no-control-regex
	if (/[\s,\u0000-\u001f\u007f]/u.test(c)) return 'No spaces, commas or control characters.';
	return '';
}

/** "#a", "#a and #b", "#a, #b and #c". */
export function listChannels(names: string[]): string {
	if (names.length <= 1) return names.join('');
	return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
}

/**
 * The auto-join preview for one role: "Members of Mods will be joined to
 * #staff and #mods". `everyone` words it for the Member role.
 */
export function previewLine(
	role: string,
	channels: RoleChannel[],
	everyone = false,
	autojoinOn = true
): string {
	const joined = channels.filter((c) => c.autojoin).map((c) => c.channel);
	const who = everyone ? 'Everyone' : `Members of ${role}`;
	if (!joined.length) {
		if (!channels.length) return everyone ? 'No channels for everyone yet.' : `${role} has no channels yet.`;
		return everyone
			? 'Nobody is joined automatically.'
			: `Members of ${role} aren’t joined anywhere automatically.`;
	}
	if (!autojoinOn)
		return `With auto-join on, ${who.charAt(0).toLowerCase() + who.slice(1)} would be joined to ${listChannels(joined)}.`;
	return `${who} will be joined to ${listChannels(joined)}.`;
}

/** Whether two channel lists differ (order doesn't matter). */
export function channelsChanged(a: RoleChannel[], b: RoleChannel[]): boolean {
	const key = (l: RoleChannel[]) =>
		l
			.map((c) => `${c.channel}:${c.autojoin ? 1 : 0}`)
			.sort()
			.join(',');
	return key(a) !== key(b);
}
