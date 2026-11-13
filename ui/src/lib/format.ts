const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
const dtf = new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'short' });
const df = new Intl.DateTimeFormat('en', { dateStyle: 'medium' });

const units: [Intl.RelativeTimeFormatUnit, number][] = [
	['year', 31536000],
	['month', 2592000],
	['week', 604800],
	['day', 86400],
	['hour', 3600],
	['minute', 60],
	['second', 1]
];

/** "3 hours ago", "in 2 days". */
export function relative(iso: string | null | undefined, now = Date.now()): string {
	if (!iso) return '';
	const diff = (new Date(iso).getTime() - now) / 1000;
	if (Math.abs(diff) < 45) return 'just now';
	for (const [unit, secs] of units) {
		if (Math.abs(diff) >= secs || unit === 'second') return rtf.format(Math.round(diff / secs), unit);
	}
	return '';
}

export function datetime(iso: string | null | undefined): string {
	return iso ? dtf.format(new Date(iso)) : '';
}

export function date(iso: string | null | undefined): string {
	return iso ? df.format(new Date(iso)) : '';
}

const nf = new Intl.NumberFormat('en', { maximumFractionDigits: 1 });
export function num(v: number | null | undefined): string {
	if (v === null || v === undefined) return '—';
	if (Math.abs(v) >= 10000)
		return new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 }).format(v);
	return nf.format(v);
}

/** A short browser + OS from a user agent, for session lists. */
export function browser(ua: string): string {
	if (!ua) return 'Unknown client';
	const b = /Edg\//.test(ua)
		? 'Edge'
		: /Firefox\//.test(ua)
			? 'Firefox'
			: /Chrome\//.test(ua)
				? 'Chrome'
				: /Safari\//.test(ua)
					? 'Safari'
					: /curl\//.test(ua)
						? 'curl'
						: /Go-http-client/.test(ua)
							? 'Go client'
							: ua.split(/[\s/]/)[0];
	const os = /Windows/.test(ua)
		? 'Windows'
		: /Mac OS X|Macintosh/.test(ua)
			? 'macOS'
			: /Android/.test(ua)
				? 'Android'
				: /iPhone|iPad/.test(ua)
					? 'iOS'
					: /Linux/.test(ua)
						? 'Linux'
						: '';
	return os ? `${b} on ${os}` : b;
}

/** Human names for audit events. */
const eventNames: Record<string, string> = {
	register: 'Registered',
	email_verified: 'Verified email',
	login: 'Signed in',
	login_failed: 'Failed sign-in',
	logout: 'Signed out',
	refresh_token_reuse: 'Refresh token reused (session revoked)',
	password_reset_requested: 'Requested password reset',
	password_reset: 'Reset password',
	password_changed: 'Changed password',
	session_revoked: 'Revoked a session',
	internal_verify_password: 'IRC password check',
	admin_user_created: 'Created a user',
	admin_user_disabled: 'Disabled a user',
	admin_user_enabled: 'Enabled a user',
	admin_role_changed: 'Changed a role',
	admin_session_revoked: 'Revoked a user session',
	admin_invite_created: 'Created an invite',
	admin_invite_revoked: 'Revoked an invite',
	signing_key_created: 'Created a signing key'
};
export function eventName(e: string): string {
	return eventNames[e] ?? e.replaceAll('_', ' ');
}

/** Tone of an audit event for its marker. */
export function eventTone(e: string): 'bad' | 'admin' | 'plain' {
	if (e === 'login_failed' || e === 'refresh_token_reuse') return 'bad';
	if (e.startsWith('admin_') || e === 'signing_key_created') return 'admin';
	return 'plain';
}
