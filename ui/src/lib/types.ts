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
	/** Their roles (most senior first); absent on auth < 1.5.0. Member is implicit. */
	roles?: RoleRef[];
	/** Effective permissions; absent on auth < 1.5.0. */
	permissions?: string[];
}

export type PermissionGroup = 'admin' | 'network' | 'irc';

export interface Permission {
	id: string;
	group: PermissionGroup | string;
	label: string;
	description: string;
}

export interface PermissionList {
	permissions: Permission[];
	/** "panel": the auth service predates roles; this is the panel's own list. */
	source: 'auth' | 'panel';
	requires?: string;
}

/** One holder of a role (GET /api/roles/{id}/members). */
export interface RoleMember {
	id: string;
	username: string;
	display_name: string;
	disabled: boolean;
	assigned_at: string | null;
	assigned_by: string | null;
}

export type Builtin = 'owner' | 'admin' | 'moderator' | 'member';

export interface RoleRef {
	id: string;
	name: string;
	color: string | null;
	position: number;
}

export interface RoleIRC {
	/** null = auth's default, "is a <name>" ("is an" before a vowel). */
	whois_line: string | null;
	/** e.g. "{username}.staff.boson.chat"; null = no vhost. */
	vhost: string | null;
	/** Effective: true whenever the role has an irc.* permission. */
	oper: boolean;
}

export interface Role extends RoleRef {
	/** The Ergo oper class name. */
	slug?: string;
	builtin: Builtin | null;
	permissions: string[];
	irc: RoleIRC;
	hoist: boolean;
	mentionable: boolean;
	member_count: number;
	created_at?: string;
	updated_at?: string;
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
	/** The stack runs its own directory (DIRECTORY_LOCAL_URL): federation switch, owned listings. */
	directory_local?: boolean;
	/** The public IRC hostname, when known. */
	irc_host?: string;
	/** The stack config file (BOSON_CONFIG) holds the runtime settings; the panel writes it. */
	config_file?: boolean;
	/** Branding image uploads work (needs the config file). */
	branding?: boolean;
	/** auth's internal email preview and test API is configured. */
	email_preview?: boolean;
	/** The files service's internal listener is configured (BOSON_FILES_INTERNAL_URL). */
	files?: boolean;
}

export interface SessionInfo {
	user: User;
	csrf_token: string;
	expires_at: string;
	features: Features;
	/** Effective permissions. The BFF enforces them; the UI hides what they don't cover. */
	permissions: string[];
	roles: RoleRef[];
	/** The auth service has roles (≥ 1.5.0). Without them role=admin means every permission. */
	roles_supported: boolean;
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
	/** auth ≥ 1.7: register a channel to its creator with ChanServ. Absent on older auth. */
	channel_auto_register?: boolean;
	/** auth ≥ 1.7: unregister channels idle this many days; 0 = never. Absent on older auth. */
	channel_expiry_days?: number;
	/** auth ≥ 1.8: IRC lets people connect without an account. Absent on older auth (guests allowed). */
	guest_access?: boolean;
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
	last_verified_at?: string;
	website_url?: string;
	auth_url?: string;
	/** local | peer:<id> */
	origin?: string;
}

/** A directory the stack's own directory imports listings from. */
export interface DirectoryPeer {
	id: string;
	url: string;
	name?: string;
	enabled: boolean;
	/** The main directory (directory.boson.chat), included by default. */
	builtin: boolean;
	/** Synced right now (enabled, and for the builtin peer, included). */
	active?: boolean;
	last_sync_at?: string;
	last_error?: string;
	listing_count: number;
}

export interface DirectorySettingValues {
	name: string;
	operator: string;
	contact: string;
	include_main: boolean;
	main_peer_url: string;
	/** Go duration, e.g. 10m. */
	sync_every: string;
}

/** The stack directory's runtime settings (directory >= 1.4.0). */
export interface DirectorySettings {
	settings: DirectorySettingValues;
	/** The directory's env values (what a reset goes back to). */
	defaults: DirectorySettingValues;
	overridden: string[];
	/** This is the main directory itself: include_main doesn't apply. */
	is_main: boolean;
	main_active: boolean;
	main_peer?: DirectoryPeer | null;
	public_url?: string;
	updated_at?: string;
	updated_by?: string | null;
}

export interface Federation {
	url: string;
	main_peer?: DirectoryPeer;
	peers: DirectoryPeer[];
	settings?: DirectorySettings;
	settings_unsupported?: string;
	settings_error?: string;
	error?: string;
	unsupported?: string;
}

export interface PeerCheck {
	url: string;
	well_known: { name: string; api: string; operator?: string; contact?: string };
}

export interface PeerSyncResult {
	federation: Federation;
	result: { created: number; updated: number; deleted: number; verified: number; error?: string };
}

export interface OwnedNetworksResponse {
	configured: boolean;
	unsupported?: string;
	error?: string;
	networks?: DirectoryServer[];
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
	/** enabled: the services shown are the compose project's containers (the Docker socket proxy). */
	discovery?: { enabled: boolean; error?: string };
}

export interface PublicProbe {
	name: string;
	url: string;
	kind: 'http' | 'ws';
}

// ---- the stack config file (config/boson.yaml) ----

export type ConfigKind =
	'bool' | 'string' | 'int' | 'enum' | 'list' | 'rooms' | 'duration' | 'text' | 'theme' | 'images';

export interface VoiceRoom {
	id: string;
	name: string;
}

export type ConfigValue = boolean | number | string | string[] | VoiceRoom[];

export interface ConfigKey {
	key: string;
	kind: ConfigKind;
	enum?: string[];
	min?: number;
	max?: number;
	max_items?: number;
	/** The permission that edits it. */
	perm: string;
	/** Compose services that read it ("*" = all). */
	services: string[];
	/** The env var that stays its default. */
	env?: string;
	/** Only applies after the service restarts. */
	restart?: boolean;
	/** The file has the key; otherwise the service uses its env / built-in default. */
	set: boolean;
	value?: ConfigValue;
	/** The env default (the panel's own keys only). */
	default?: ConfigValue;
	line?: number;
	editable: boolean;
}

export interface ConfigProblem {
	message: string;
	line?: number;
	column?: number;
}

export interface ConfigIssue extends ConfigProblem {
	key: string;
}

export interface ConfigView {
	enabled: boolean;
	path?: string;
	etag?: string;
	exists: boolean;
	modified_at?: string;
	loaded_at?: string;
	saved_by?: string;
	saved_at?: string;
	/** The file on disk doesn't parse: everyone keeps the last good version. */
	error?: ConfigProblem;
	/** Values that parse but aren't valid. */
	issues: ConfigIssue[];
	keys: ConfigKey[];
}

export interface ConfigRaw {
	path: string;
	exists: boolean;
	content: string;
	etag: string;
	error?: ConfigProblem;
}

export type ConfigState =
	'applied' | 'pending' | 'restart_required' | 'error' | 'unreachable' | 'unsupported';

export interface ConfigServiceStatus {
	name: string;
	state: ConfigState;
	loaded_at?: string;
	file_mtime?: string;
	error?: string;
	restart_required: string[];
	sections: string[];
}

export interface ConfigChange {
	/** Theme documents (branding.theme) are objects. */
	set?: Record<string, ConfigValue | object>;
	unset?: string[];
}
