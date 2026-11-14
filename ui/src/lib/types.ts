export interface User {
	id: string;
	username: string;
	email: string | null;
	email_verified: boolean;
	display_name: string;
	role: 'user' | 'admin';
	created_at: string;
	disabled_at?: string;
}

export interface Features {
	version: string;
	grafana_url?: string;
	metrics: boolean;
}

export interface SessionInfo {
	user: User;
	csrf_token: string;
	expires_at: string;
	features: Features;
}

export interface UserSession {
	id: string;
	user_id: string;
	auth_method: string;
	ip: string;
	user_agent: string;
	created_at: string;
	last_seen_at: string;
	expires_at: string;
	current: boolean;
}

export interface Invite {
	id: string;
	code?: string;
	note: string;
	max_uses: number;
	uses: number;
	created_by: string | null;
	expires_at: string | null;
	revoked_at?: string;
	created_at: string;
}

export interface AuditEntry {
	id: string;
	user_id: string | null;
	event: string;
	ip: string;
	meta: Record<string, unknown> | null;
	at: string;
	username?: string;
	target_user_id?: string;
	target_username?: string;
}

export interface HealthCheck {
	url: string;
	ok: boolean;
	status_code?: number;
	latency_ms: number;
	detail?: string;
}

export interface ServiceHealth {
	name: string;
	status: '' | 'up' | 'degraded' | 'down';
	version?: string;
	version_error?: string;
	checks: HealthCheck[];
}

export interface Certificate {
	target: string;
	server_name: string;
	address: string;
	status: 'ok' | 'warning' | 'critical' | 'expired' | 'error';
	days_left?: number;
	error?: string;
	checked_at: string;
	cert?: {
		subject: string;
		issuer: string;
		dns_names: string[];
		not_before: string;
		not_after: string;
		trusted: boolean;
		verify_error?: string;
	};
}

export interface Stat {
	key: string;
	label: string;
	group: 'voice' | 'auth';
	unit?: string;
	query: string;
	value: number | null;
	breakdown?: { label: string; value: number }[];
	trend?: [number, number][];
	error?: string;
}

export interface Overview {
	checked_at: string;
	panel: { version: string };
	services: ServiceHealth[];
	certificates: Certificate[];
	metrics: { configured: boolean; error?: string; stats: Stat[] };
	grafana_url?: string;
	edge: { name: string };
	irc: { configured: boolean; target?: string };
	public_probes?: PublicProbe[];
}

export interface PublicProbe {
	name: string;
	url: string;
	kind: 'http' | 'ws';
}
