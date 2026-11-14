import { api } from './api';
import type { ConfigChange, ConfigKey, ConfigServiceStatus, ConfigView } from './types';

/**
 * The stack config file as the panel last saw it, shared by the Stack
 * config page, the banner in the shell and the inline editors on the
 * Directory and Stack pages.
 */
class ConfigState {
	view = $state<ConfigView | null>(null);
	error = $state('');
	/** Services' answers after the last save (or the last check). */
	statuses = $state<ConfigServiceStatus[] | null>(null);

	async load() {
		try {
			this.view = await api<ConfigView>('GET', '/config');
			this.error = '';
		} catch (e) {
			this.error = e instanceof Error ? e.message : String(e);
		}
		return this.view;
	}

	key(name: string): ConfigKey | undefined {
		return this.view?.keys.find((k) => k.key === name);
	}

	/** Saves a change against the version on screen (If-Match). */
	async save(change: ConfigChange) {
		const etag = this.view?.etag;
		this.view = await api<ConfigView>('PATCH', '/config', change, etag ? { 'If-Match': etag } : undefined);
		return this.view;
	}

	async checkServices() {
		this.statuses = (await api<{ services: ConfigServiceStatus[] }>('GET', '/config/status')).services;
		return this.statuses;
	}
}

export const stackConfig = new ConfigState();

/** Groups of keys on the Stack config page, in order. */
export const configGroups: { id: string; title: string; detail: string; prefix: string[] }[] = [
	{
		id: 'admin',
		title: 'Admin panel',
		detail: 'What this panel lists in the directory and which certificates the Stack page watches.',
		prefix: ['admin.']
	},
	{ id: 'network', title: 'Network', detail: 'Shown by every service.', prefix: ['network.'] },
	{
		id: 'voice',
		title: 'Voice rooms',
		detail: 'The rooms the voice server always has, and its limits.',
		prefix: ['voice.']
	},
	{
		id: 'irc',
		title: 'IRC options',
		detail:
			'Read by the Ergo helper, which re-renders ircd.yaml and rehashes Ergo. The plaintext port stays in the file and compose, since the host publishes it.',
		prefix: ['irc.']
	},
	{
		id: 'auth',
		title: 'Email and browser access',
		detail:
			'The auth service’s outgoing email and which other sites may call it. The SMTP user and password stay in .env.',
		prefix: ['auth.smtp.', 'auth.cors_allowed_origins']
	}
];

/** Human labels for keys. */
export const keyLabels: Record<string, { label: string; detail?: string }> = {
	'admin.directory_registration': {
		label: 'Directory registration',
		detail:
			'List and edit this network in the boson directory from the Directory page. Off: the page only checks whether it is listed.'
	},
	'admin.directory_hostname': {
		label: 'Hostname to list',
		detail:
			'The public IRC hostname the Directory page lists and checks. Its DNS TXT record proves you run it.'
	},
	'admin.tls_check_hosts': {
		label: 'Certificates to watch',
		detail:
			'One host:port per line, checked every 5 minutes on the Stack page. Add @dial-host:port to connect somewhere else, like irc.example.org:443@traefik:443.'
	},
	'network.name': { label: 'Network name', detail: 'In emails, the IRC welcome and the directory.' },
	'voice.rooms': { label: 'Static rooms', detail: 'Always there, in this order.' },
	'voice.rooms_strict': {
		label: 'Only these rooms',
		detail: 'On: people can’t open rooms beyond these and the ones channel founders add.'
	},
	'voice.max_rooms_per_user': {
		label: 'Rooms per person',
		detail: 'How many rooms one person may open. 0 = none.'
	},
	'voice.max_rooms': {
		label: 'Rooms in total',
		detail: 'The most rooms the server holds at once. 0 = no limit.'
	},
	'voice.max_participants': {
		label: 'People per room',
		detail: 'The most people in one room. 0 = no limit.'
	},
	'irc.plaintext': {
		label: 'Plaintext IRC',
		detail:
			'Accept unencrypted connections on port 6667 (published on the host by compose). Needs an Ergo restart.'
	},
	'irc.require_sasl_exempt': {
		label: 'Exempt from sign-in',
		detail: 'IPs or CIDRs that may connect without an account while guest access is off. One per line.'
	},
	'irc.websocket': { label: 'Web clients', detail: 'Accept WebSocket connections at wss://IRC_HOST/webirc.' },
	'irc.websocket_origins': {
		label: 'Web client origins',
		detail: 'Websites allowed to open those WebSockets. Empty = any. One per line.'
	},
	'irc.oauthbearer': {
		label: 'Token sign-in',
		detail: 'Let apps sign in to IRC with an auth token (SASL OAUTHBEARER).'
	},
	'irc.persistent_history': {
		label: 'Keep history on disk',
		detail: 'Channel history survives Ergo restarts. Needs an Ergo restart.'
	},
	'irc.history_expire': { label: 'History kept for', detail: 'Like 7d, 30d or 1w. 0 = forever.' },
	'irc.log_level': { label: 'Ergo log level' },
	'irc.roles_poll_seconds': { label: 'Role sync interval', detail: 'Seconds between role checks with auth.' },
	'irc.settings_poll_seconds': {
		label: 'Settings sync interval',
		detail: 'Seconds between checks of auth’s channel and guest settings.'
	},
	'auth.smtp.host': { label: 'SMTP host' },
	'auth.smtp.port': { label: 'SMTP port' },
	'auth.smtp.from': { label: 'From address', detail: 'Like Boson <hi@example.com>.' },
	'auth.smtp.tls': { label: 'SMTP encryption' },
	'auth.cors_allowed_origins': {
		label: 'Allowed browser origins',
		detail: 'Sites that may sign people in through auth, like your directory’s website. One per line.'
	}
};

export function label(key: string) {
	return keyLabels[key]?.label ?? key;
}

/** Service display names. */
export const serviceNames: Record<string, string> = {
	admin: 'Admin panel',
	auth: 'Auth',
	ergo: 'IRC (Ergo)',
	quicvoiced: 'Voice',
	directory: 'Directory'
};
