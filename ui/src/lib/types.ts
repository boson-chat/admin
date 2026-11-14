export interface User {
	id: string;
	username: string;
	email: string | null;
	email_verified: boolean;
	display_name: string;
	role: 'user' | 'admin';
	created_at: string;
	disabled_at?: string;
	/** Linked SSO sign-ins; absent on auth releases that don't report them (< 1.4.0). */
	identities?: Identity[];
	/** False for SSO-only accounts; absent on auth < 1.4.0. */
	has_password?: boolean;
}

export interface Identity {
	id: string;
	provider_id: string;
	provider_slug: string;
	provider_kind: 'oidc' | 'saml' | string;
	provider_display_name: string;
	subject: string;
	email: string | null;
	created_at?: string;
	last_login_at: string | null;
}

export interface Features {
	version: string;
	grafana_url?: string;
	metrics: boolean;
	services: boolean;
	profiles: boolean;
	directory: boolean;
	directory_registration: boolean;
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
	actor_username?: string;
	target_user_id?: string;
	target_username?: string;
}

export interface AuditPage {
	entries: AuditEntry[];
	events: string[];
	next_cursor?: string;
	server_filtered: boolean;
	requires?: string;
}

export type Registration = 'open' | 'invite' | 'closed';
export type TextCreators = 'registered' | 'admins';
export type VoiceCreators = 'admins' | 'founders' | 'registered';

export interface NetworkSettings {
	registration: Registration;
	require_email_verification: boolean;
	channel_creation: TextCreators;
	voice_channel_creation: VoiceCreators;
	updated_at?: string;
	updated_by?: string;
	updated_by_username?: string;
}

export interface OIDCConfig {
	issuer: string;
	client_id: string;
	/** Write-only; sent only when changed. */
	client_secret?: string;
	scopes?: string[];
	username_claim?: string;
	email_claim?: string;
	display_name_claim?: string;
}

export interface SAMLConfig {
	idp_metadata_url?: string;
	idp_metadata_xml?: string;
	entity_id?: string;
	attribute_map: { email?: string; username?: string; display_name?: string };
	name_id_format?: string;
	sign_requests?: boolean;
}

export type ProviderKind = 'oidc' | 'saml';

export interface SSOProvider {
	id: string;
	slug: string;
	kind: ProviderKind;
	display_name: string;
	enabled: boolean;
	allow_signup: boolean;
	trust_email: boolean;
	config: { oidc?: OIDCConfig; saml?: SAMLConfig };
	has_client_secret: boolean;
	start_url?: string;
	/** OIDC: the redirect (callback) URI to register at the IdP. */
	redirect_uri?: string;
	sp_metadata_url?: string;
	acs_url?: string;
	sp_entity_id?: string;
	/** SAML: the SP signing certificate (PEM), for IdPs that want it. */
	sp_certificate?: string;
	created_at?: string;
	updated_at?: string;
}

export interface ServiceInfo {
	name: string;
	service: string;
	image: string;
	version?: string;
	state: 'running' | 'exited' | 'restarting' | 'paused' | 'created' | 'dead' | 'removing' | string;
	status: string;
	health?: 'healthy' | 'unhealthy' | 'starting' | string;
	started_at?: string;
	finished_at?: string;
	exit_code: number;
	oom_killed?: boolean;
	restart_count: number;
	cpu_percent?: number;
	memory_bytes?: number;
	memory_limit?: number;
	protected: boolean;
	self: boolean;
}

export interface ServiceList {
	configured: boolean;
	project?: string;
	services: ServiceInfo[];
	error?: string;
	checked_at: string;
}

export interface LogLine {
	at: string;
	stream: 'stdout' | 'stderr';
	text: string;
}

export interface BosonProfile {
	user_id: string;
	auth_user_id: string;
	handle: string;
	display_name?: string;
	avatar_url?: string;
	is_discoverable: boolean;
	handle_changed_at?: string;
	created_at?: string;
}

export interface DirectoryServer {
	id: string;
	hostname: string;
	port: number;
	tls: boolean;
	name: string;
	description?: string;
	tags?: string[];
	languages?: string[];
	is_nsfw: boolean;
	verification_status: 'pending' | 'verified' | 'lapsed' | string;
	verification_last_checked_at?: string;
	health_status?: 'up' | 'down' | 'unknown' | string;
	user_count?: number;
	icon_url?: string;
	registered_at?: string;
	website_url?: string;
	auth_url?: string;
}

export interface ProfileResponse {
	configured: boolean;
	unsupported?: string;
	error?: string;
	profile?: BosonProfile | null;
	servers?: DirectoryServer[];
}

export interface Verification {
	record_name: string;
	record_value: string;
	expires_at?: string;
}

export interface Listing {
	network: DirectoryServer;
	verification?: Verification;
}

export interface DirectoryStatus {
	api_url: string;
	registration: boolean;
	hostname: string;
	port: number;
	public?: DirectoryServer;
	public_error?: string;
	listing?: Listing;
	listing_error?: string;
	unsupported?: string;
}

export interface VerifyResult {
	success: boolean;
	report: {
		success: boolean;
		results: Record<
			string,
			{
				outcome: 'match' | 'missing_record' | 'timeout' | 'error' | string;
				detail?: string;
				records?: string[];
			}
		>;
	};
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
