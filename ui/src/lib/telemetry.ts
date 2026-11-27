// Usage & health stats (the stack's docs/telemetry.md): the switch, the last
// send and the next report as the panel would send it.

/** The Boson team's why, shown first on the card (the spec's short text). */
export const TELEMETRY_WHY =
	'We collect a small amount of anonymous usage and health data to make Boson better: to see which versions and features people use, and to catch failed updates, crashes and struggling servers so we can fix them, often before anyone has to report them. No names, emails, messages, channel names, IP addresses or hostnames, ever.';

export const TELEMETRY_INFO_URL = 'https://boson.chat/telemetry';

export type TelemetryResult = {
	at: string;
	reason: string;
	ok: boolean;
	status?: number;
	error?: string;
};

/** GET /api/telemetry. */
export type TelemetryStatus = {
	enabled: boolean;
	/** TELEMETRY=off in .env: the switch is off and can't change that. */
	locked: boolean;
	file_on: boolean;
	config_file: boolean;
	endpoint: string;
	last_sent_at?: string;
	last?: TelemetryResult;
	next_at?: string;
	next_reason?: string;
};

const errors: Record<string, string> = {
	timeout: 'the stats server didn’t answer in time',
	dns: 'the stats server’s name didn’t resolve',
	refused: 'the connection was refused',
	tls: 'the TLS handshake failed',
	network: 'a network error',
	too_large: 'the report was too large to send',
	build: 'the report couldn’t be put together'
};

/** The last send's outcome in words. */
export function resultText(r: TelemetryResult | undefined): string {
	if (!r) return 'Nothing sent yet';
	if (r.ok) return 'Received';
	if (r.status) return `Refused (HTTP ${r.status})`;
	return `Not sent: ${errors[r.error ?? ''] ?? 'a network error'}`;
}

/** Whether the last send went through (for the tone of the line). */
export function resultTone(r: TelemetryResult | undefined): 'ok' | 'bad' | 'none' {
	if (!r) return 'none';
	return r.ok ? 'ok' : 'bad';
}

const reasons: Record<string, string> = {
	setup: 'the first report of this install',
	daily: 'the daily report',
	update: 'after an update',
	rollback: 'after a rollback'
};

export function reasonText(r: string | undefined): string {
	return reasons[r ?? ''] ?? 'the daily report';
}

/** The endpoint's host, for "goes to telemetry.boson.chat". */
export function endpointHost(url: string | undefined): string {
	try {
		return new URL(url ?? '').host || 'telemetry.boson.chat';
	} catch {
		return 'telemetry.boson.chat';
	}
}
