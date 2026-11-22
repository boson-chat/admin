import type { NetworkChannel, RoleChannel } from './types';

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

// ---- network channels (irc.channels in the stack config file) ----

/** Config keys of the network channels. */
export const NETWORK_CHANNELS_KEY = 'irc.channels';
export const CHANNEL_FOUNDER_KEY = 'irc.channel_founder';
export const AUTOJOIN_GUESTS_KEY = 'irc.autojoin_guests';
export const MAX_NETWORK_CHANNELS = 100;
export const MAX_TOPIC = 300;

/** A channel name as Ergo matches network channels: ASCII casefolding. */
export function foldAscii(s: string): string {
	return s.trim().replace(/[A-Z]/g, (c) => c.toLowerCase());
}

/**
 * Why `s` isn't a network channel name, or '' (mirrors the BFF): #, then
 * 1–63 printable ASCII characters without spaces or commas.
 */
export function networkChannelProblem(s: string, taken: string[] = []): string {
	const c = s.trim();
	if (!c) return 'Type a channel name, like #lobby.';
	if (!c.startsWith('#')) return 'A channel name starts with #, like #lobby.';
	if (c.length === 1) return 'Add a name after the #.';
	if (c.length > MAX_CHANNEL) return 'At most 64 characters.';
	if (/[ ,]/.test(c)) return 'No spaces or commas.';
	if (!/^[\x21-\x7e]+$/.test(c)) return 'Only letters, digits and ASCII punctuation (no accents or emoji).';
	if (taken.some((t) => foldAscii(t) === foldAscii(c))) return `${c} is already listed.`;
	return '';
}

/** Why a topic can't be saved, or ''. */
export function topicProblem(s: string): string {
	if ([...s.trim()].length > MAX_TOPIC) return `At most ${MAX_TOPIC} characters.`;
	if (/[\r\n]/.test(s)) return 'One line only.';
	return '';
}

/** The list as the file gets it: trimmed topics, nothing else. */
export function cleanNetworkChannels(l: NetworkChannel[]): NetworkChannel[] {
	return l.map((c) => {
		const topic = (c.topic ?? '').trim();
		const out: NetworkChannel = topic
			? { name: c.name.trim(), topic, autojoin: c.autojoin }
			: { name: c.name.trim(), autojoin: c.autojoin };
		const roles = cleanChannelRoles(c.roles);
		if (roles.length) out.roles = roles;
		return out;
	});
}

/** A channel's roles trimmed, without blanks and case-insensitive repeats. */
export function cleanChannelRoles(roles: readonly string[] | undefined): string[] {
	const out: string[] = [];
	for (const r of roles ?? []) {
		const t = r.trim();
		if (t && !out.some((x) => x.toLowerCase() === t.toLowerCase())) out.push(t);
	}
	return out;
}

/** A role as a network channel's roles can name it (auth matches the name or the slug). */
export interface ChannelRole {
	name: string;
	slug?: string;
	builtin?: string | null;
}

/** The role a channel's roles entry names, if any (case-insensitive, name or slug). */
export function matchChannelRole<R extends ChannelRole>(entry: string, roles: readonly R[]): R | undefined {
	const e = entry.trim().toLowerCase();
	const slug = e.replace(/ /g, '-');
	return roles.find(
		(r) => r.name.toLowerCase() === e || (r.slug ?? '').toLowerCase() === e || (r.slug ?? '') === slug
	);
}

/**
 * Who may join, in words: "Everyone" (guests too), "All accounts" (Member
 * listed: no guests), "Admin" / "Admin or Owner" / "3 roles".
 */
export function channelAccessLabel(
	entries: readonly string[] | undefined,
	roles: readonly ChannelRole[]
): string {
	const list = cleanChannelRoles(entries);
	if (!list.length) return 'Everyone';
	const matched = list.map((e) => matchChannelRole(e, roles));
	if (matched.some((r) => r?.builtin === 'member')) return 'All accounts';
	const names = list.map((e, i) => matched[i]?.name ?? e);
	if (names.length === 1) return names[0];
	if (names.length === 2) return `${names[0]} or ${names[1]}`;
	return `${names.length} roles`;
}

/** Whether two network channel lists differ (order counts: it's the file's). */
export function networkChannelsChanged(a: NetworkChannel[], b: NetworkChannel[]): boolean {
	return JSON.stringify(cleanNetworkChannels(a)) !== JSON.stringify(cleanNetworkChannels(b));
}

/** Why `s` can't be irc.channel_founder, or '' ('' = the network owner). */
export function founderProblem(s: string): string {
	const f = s.trim();
	if (!f) return '';
	if (f.length > 64 || /[\s,*?!@:$%#&]/.test(f)) return 'An account name, like thelion.';
	return '';
}
