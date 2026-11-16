// A stand-in auth service for the Emails page's browser checks: enough of
// the public API to sign in (one legacy admin), and an internal listener
// whose email preview echoes what it's sent into an MJML-like layout (with
// a dark-mode media query), so the spec can see unsaved settings arrive.
// Unsaved compiled MJML (layout_html, html) is filled in like auth does,
// with a warning per unknown {{placeholder}}; GET default-mjml serves a
// small default layout.
//
//   node e2e/fake-auth.mjs            # public :18680, internal :18681
//   FAKE_AUTH_ROLES=1 node e2e/fake-auth.mjs   # an auth with roles (admin is
//                                          # Owner) and role channels
//   POST <internal>/__mode {"mode":"old"}  # act like auth 1.10.x: ignore
//                                          # "email", send no from, no
//                                          # last sign-in / activity
//
// Its password is "e2e-password" for the user "admin".
import { createServer } from 'node:http';
import process from 'node:process';

const PUBLIC = Number(process.env.FAKE_AUTH_PORT ?? 18680);
const INTERNAL = Number(process.env.FAKE_AUTH_INTERNAL_PORT ?? 18681);
const PASSWORD = 'e2e-password';

const user = {
	id: 'usr_admin',
	username: 'admin',
	email: 'admin@example.com',
	email_verified: true,
	display_name: 'Admin',
	role: 'admin',
	created_at: '2026-01-01T00:00:00Z'
};
const ago = (min) => new Date(Date.now() - min * 60_000).toISOString();
const activity = (signIn, method, ip, active) => ({
	last_sign_in_at: signIn === null ? null : ago(signIn),
	last_sign_in_method: method,
	last_sign_in_ip: ip,
	last_active_at: active === null ? null : ago(active)
});
const member = (id, username, display_name, created_at) => ({
	id,
	username,
	email: `${username}@example.com`,
	email_verified: true,
	display_name,
	role: 'user',
	created_at
});
// The users list: newest account first, like auth.
function people() {
	const list = [
		{
			...member('usr_new', 'newcomer', 'Newcomer', '2026-11-15T18:00:00Z'),
			...activity(null, null, null, null)
		},
		{
			...member('usr_maya', 'maya', 'Maya Lin', '2026-06-12T09:00:00Z'),
			...activity(2 * 1440, 'oidc:corp', '198.51.100.7', 3 * 60)
		},
		{
			...member('usr_ravi', 'ravi', 'Ravi', '2026-03-01T09:00:00Z'),
			disabled_at: '2026-10-10T09:00:00Z',
			...activity(40 * 1440, 'saml:okta', '', 38 * 1440)
		},
		{ ...user, ...activity(190, 'password', '203.0.113.24', 3) }
	];
	if (mode === 'old')
		for (const u of list)
			for (const k of ['last_sign_in_at', 'last_sign_in_method', 'last_sign_in_ip', 'last_active_at'])
				delete u[k];
	return list;
}
const access = new Set();
const refresh = new Set();
let n = 0;
let mode = 'new';
const ROLES = process.env.FAKE_AUTH_ROLES === '1';
const PANEL_PERMS = [
	'admin.panel',
	'audit.view',
	'directory.manage',
	'invites.manage',
	'roles.manage',
	'roles.owner',
	'services.manage',
	'services.view',
	'settings.manage',
	'sso.manage',
	'users.manage',
	'users.moderate',
	'users.view'
];
const role = (id, name, color, position, builtin, permissions, member_count) => ({
	id,
	name,
	slug: name.toLowerCase(),
	color,
	position,
	builtin,
	permissions,
	irc: { whois_line: null, vhost: null, oper: position >= 500 },
	hoist: position >= 500,
	mentionable: false,
	member_count
});
const roles = [
	role('rol_owner', 'Owner', '#f1c40f', 1000, 'owner', PANEL_PERMS, 1),
	role(
		'rol_admin',
		'Admin',
		'#e74c3c',
		900,
		'admin',
		PANEL_PERMS.filter((p) => p !== 'roles.owner'),
		0
	),
	role('rol_mods', 'Mods', '#3498db', 500, null, ['users.view', 'users.moderate'], 4),
	role('rol_events', 'Event hosts', '#9b59b6', 200, null, [], 2),
	role('rol_member', 'Member', null, 0, 'member', ['channel.create', 'voice.create'], 12)
];
// role id (or "everyone") → [{channel, autojoin}]; [{channel, private}]
const initialRoleChannels = {
	rol_mods: [
		{ channel: '#mods', autojoin: true },
		{ channel: '#staff', autojoin: true },
		{ channel: '#mod-log', autojoin: false }
	],
	rol_admin: [{ channel: '#staff', autojoin: true }],
	everyone: [
		{ channel: '#lobby', autojoin: true },
		{ channel: '#help', autojoin: false }
	]
};
const initialAccess = [
	{ channel: '#staff', private: true },
	{ channel: '#mod-log', private: true }
];
let roleChannels = structuredClone(initialRoleChannels);
let channelAccess = structuredClone(initialAccess);
const audit = [];
const sortCh = (l) => [...l].sort((a, b) => a.channel.localeCompare(b.channel));
const saved = { sender_name: 'Example Chat', accent: '#3b5bdb', footer: '', logo: true };

function tokens() {
	n++;
	const at = `at-${n}`;
	const rt = `rt-${n}`;
	access.add(at);
	refresh.add(rt);
	return {
		token_type: 'Bearer',
		access_token: at,
		expires_in: 900,
		refresh_token: rt,
		refresh_expires_in: 86400,
		session_id: 'ses_1',
		user
	};
}

async function json(req) {
	let b = '';
	for await (const c of req) b += c;
	try {
		return b ? JSON.parse(b) : {};
	} catch {
		return {};
	}
}

function send(res, status, body) {
	res.writeHead(status, { 'Content-Type': 'application/json' });
	res.end(body === undefined ? '' : JSON.stringify(body));
}

const bearer = (req) => (req.headers.authorization ?? '').replace(/^Bearer /, '');
const esc = (s) =>
	String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]);

const defaults = {
	verification: ['Confirm your email for {{network}}', 'Hi {{username}},\n\nConfirm your address: {{link}}'],
	password_reset: ['Reset your {{network}} password', 'Hi {{username}},\n\nReset it here: {{link}}'],
	account_setup: ['Set up your {{network}} account', 'Hi {{username}},\n\nChoose a password: {{link}}'],
	invite: ['{{inviter}} invited you to {{network}}', 'Hi,\n\n{{inviter}} invited you: {{link}}']
};
const vars = {
	network: 'Example Chat',
	username: 'alice',
	inviter: 'bob',
	link: 'https://chat.example.com/verify?token=example',
	expires: '24 hours'
};
const fillVars = (s) => s.replace(/\{\{\s*([a-z_]+)\s*\}\}/g, (m, k) => vars[k] ?? m);

// Boson's default MJML (GET /internal/email/default-mjml): a small layout
// with {{content}}, and per-email documents built from it.
const DEFAULT_LAYOUT = `<mjml>
  <mj-head>
    <mj-title>{{subject}}</mj-title>
    <mj-attributes>
      <mj-all font-family="Helvetica, Arial, sans-serif" />
    </mj-attributes>
  </mj-head>
  <mj-body background-color="#f1f2f4">
    <mj-section padding="24px 0 8px">
      <mj-column>
        <mj-text font-size="18px" font-weight="600" color="{{accent}}" css-class="banner">{{network}}</mj-text>
      </mj-column>
    </mj-section>
    <mj-section background-color="#ffffff" border-radius="8px" padding="8px 12px">
      <mj-column>
        <mj-text font-size="15px" line-height="1.55">{{content}}</mj-text>
      </mj-column>
    </mj-section>
    <mj-section>
      <mj-column>
        <mj-text align="center" font-size="12px" color="#6b7280">{{footer}}</mj-text>
      </mj-column>
    </mj-section>
  </mj-body>
</mjml>
`;
function defaultMJML(part) {
	if (part === 'layout') return DEFAULT_LAYOUT;
	const [, body] = defaults[part];
	const text = esc(body.replace('{{link}}', '').trim()).replace(/\n/g, '<br />');
	return DEFAULT_LAYOUT.replace(
		'{{content}}',
		`${text}</mj-text>\n        <mj-button href="{{link}}" background-color="{{accent}}">Open link</mj-button>\n        <mj-text>`
	);
}

const KNOWN = [
	'content',
	'subject',
	'network',
	'username',
	'inviter',
	'expires',
	'link',
	'logo_url',
	'accent',
	'footer',
	'sender_name'
];

/** Fills the placeholders of custom HTML in one pass, noting unknown ones. */
function fillHTML(html, values, warnings) {
	return html.replace(/\{\{\s*([A-Za-z_]\w*)\s*\}\}/g, (m, k) => {
		if (k in values) return values[k];
		if (!KNOWN.includes(k)) warnings.push(`{{${k}}} isn’t a placeholder; it’s sent as typed`);
		return m;
	});
}

function render(r) {
	const e = mode === 'old' ? saved : { ...saved, ...(r.email ?? {}) };
	const [ds, db] = defaults[r.template];
	const subject = fillVars(r.subject ?? ds);
	const body = fillVars(r.body ?? db);
	const paras = body
		.split(/\n\s*\n/)
		.map((p) =>
			esc(p)
				.replace(/https?:\/\/\S+/g, (u) => `<a class="btn" href="${u}">Open link</a>`)
				.replace(/\n/g, '<br>')
		)
		.map((p) => `<p>${p}</p>`)
		.join('');
	const custom = mode === 'old' ? undefined : (r.html ?? r.layout_html);
	if (custom) {
		const warnings = [];
		const values = {
			...Object.fromEntries(Object.entries(vars).map(([k, v]) => [k, esc(v)])),
			subject: esc(subject),
			accent: e.accent,
			footer: esc(e.footer || 'Example Chat'),
			sender_name: esc(e.sender_name || 'Example Chat'),
			logo_url: 'https://auth.example.com/v1/branding/assets/logo.webp'
		};
		if (!r.html) values.content = `<div data-content>${paras}</div>`;
		const html = fillHTML(custom, values, warnings);
		const text = `${body}\n\n-- \n${e.footer || 'Example Chat'}`;
		return {
			subject,
			html,
			text,
			from: `${e.sender_name || 'Example Chat'} <no-reply@example.com>`,
			warnings
		};
	}
	const logo = e.logo
		? `<div class="logo"><span class="mark"></span><strong>Example Chat</strong></div>`
		: '';
	const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<style>
body{margin:0;padding:0;background:#f1f2f4;font-family:Helvetica,Arial,sans-serif;color:#1f2328}
.wrap{max-width:560px;margin:0 auto;padding:24px 16px}
.card{background:#ffffff;border-radius:8px;padding:28px 28px 20px;border-top:4px solid ${e.accent}}
.logo{display:flex;align-items:center;gap:10px;margin-bottom:20px;font-size:18px}
.mark{display:inline-block;width:28px;height:28px;border-radius:7px;background:${e.accent}}
p{font-size:15px;line-height:1.55;margin:0 0 14px}
.btn{display:inline-block;background:${e.accent};color:#ffffff !important;text-decoration:none;padding:10px 18px;border-radius:6px;font-weight:600}
.foot{text-align:center;font-size:12px;color:#6b7280;padding:16px 8px}
@media (max-width: 480px){.card{padding:20px 16px}.wrap{padding:12px 8px}}
@media (prefers-color-scheme: dark){body{background:#121214;color:#e6e6ea}.card{background:#1c1c21}.foot{color:#9a9aa6}}
</style></head><body><div class="wrap"><div class="card" data-accent="${esc(e.accent)}">${logo}${paras}</div>
<div class="foot" data-footer>${esc(e.footer || 'Example Chat')}</div></div></body></html>`;
	const text = `${body}\n\n-- \n${e.footer || 'Example Chat'}`;
	const out = { subject, html, text };
	if (mode !== 'old') out.from = `${e.sender_name || 'Example Chat'} <no-reply@example.com>`;
	return out;
}

createServer(async (req, res) => {
	const url = new URL(req.url, 'http://x');
	const p = url.pathname;
	if (req.method === 'POST' && p === '/v1/login') {
		const b = await json(req);
		if (b.login !== 'admin' || b.password !== PASSWORD)
			return send(res, 401, { error: 'invalid_credentials', message: 'wrong username or password' });
		return send(res, 200, tokens());
	}
	if (req.method === 'POST' && p === '/v1/token/refresh') {
		const b = await json(req);
		if (!refresh.delete(b.refresh_token))
			return send(res, 401, { error: 'invalid_token', message: 'expired' });
		return send(res, 200, tokens());
	}
	if (req.method === 'POST' && p === '/v1/logout') return send(res, 204);
	if (req.method === 'GET' && p === '/v1/me') {
		if (!access.has(bearer(req))) return send(res, 401, { error: 'unauthorized' });
		if (ROLES)
			return send(res, 200, {
				user,
				roles: [{ id: 'rol_owner', name: 'Owner', color: '#f1c40f', position: 1000 }],
				permissions: PANEL_PERMS
			});
		return send(res, 200, { user });
	}
	if (
		ROLES &&
		p.startsWith('/v1/admin/') &&
		(p.startsWith('/v1/admin/roles') ||
			p.startsWith('/v1/admin/permissions') ||
			p.startsWith('/v1/admin/channel-access'))
	) {
		if (!access.has(bearer(req))) return send(res, 401, { error: 'unauthorized' });
		if (req.method === 'GET' && p === '/v1/admin/permissions')
			return send(res, 200, {
				permissions: PANEL_PERMS.map((id) => ({ id, group: 'admin', label: id, description: '' }))
			});
		if (req.method === 'GET' && p === '/v1/admin/roles') return send(res, 200, { roles });
		if (p === '/v1/admin/channel-access') {
			if (req.method === 'PUT') {
				channelAccess = sortCh((await json(req)).channels ?? []);
				audit.push({ event: 'channel_access_updated', channels: channelAccess });
			}
			return send(res, 200, { channels: channelAccess });
		}
		const m = p.match(/^\/v1\/admin\/roles\/([^/]+)(\/channels|\/members)?$/);
		if (m) {
			const id = decodeURIComponent(m[1]);
			const r = roles.find((x) => x.id === id);
			if (!r && id !== 'everyone')
				return send(res, 404, { error: 'role_not_found', message: 'no such role' });
			if (m[2] === '/channels') {
				if (req.method === 'PUT') {
					roleChannels[id] = sortCh((await json(req)).channels ?? []);
					audit.push({ event: 'role_channels_updated', role_id: id, channels: roleChannels[id] });
				}
				return send(res, 200, { channels: roleChannels[id] ?? [] });
			}
			if (m[2] === '/members') return send(res, 200, { users: [], next_cursor: null });
			if (req.method === 'GET') return send(res, 200, { role: r });
		}
	}
	// The user reads the Users pages need (uploads and users e2e). Newer auth
	// reports last sign-in / activity and sorts by it; mode "old" doesn't.
	if (req.method === 'GET' && p.startsWith('/v1/admin/')) {
		if (!access.has(bearer(req))) return send(res, 401, { error: 'unauthorized' });
		if (p === '/v1/admin/users') {
			const sort = new URL(req.url, 'http://x').searchParams.get('sort');
			const list = people();
			const key = { last_active: 'last_active_at', last_sign_in: 'last_sign_in_at' }[
				mode === 'old' ? '' : sort
			];
			if (key) list.sort((a, b) => (b[key] ?? '').localeCompare(a[key] ?? ''));
			return send(res, 200, { users: list, total: list.length });
		}
		const m = p.match(/^\/v1\/admin\/users\/([^/]+)(\/sessions)?$/);
		const found = m && people().find((u) => u.id === decodeURIComponent(m[1]));
		if (found && m[2]) return send(res, 200, { sessions: [] });
		if (found) return send(res, 200, { user: found, identities: [], has_password: true });
		if (p === '/v1/admin/audit') return send(res, 200, { entries: [] });
	}
	if (req.method === 'GET' && p === '/v1/config')
		return send(res, 200, { registration: 'invite', require_email_verification: true, sso_providers: [] });
	send(res, 404, { error: 'not_found', message: 'not found' });
}).listen(PUBLIC, '127.0.0.1');

createServer(async (req, res) => {
	const p = new URL(req.url, 'http://x').pathname;
	if (p === '/healthz' || p === '/readyz') return send(res, 200, { status: 'ok' });
	if (req.method === 'GET' && p === '/__audit') return send(res, 200, { audit });
	if (req.method === 'POST' && p === '/__reset') {
		roleChannels = structuredClone(initialRoleChannels);
		channelAccess = structuredClone(initialAccess);
		audit.length = 0;
		return send(res, 200, {});
	}
	if (req.method === 'POST' && p === '/__mode') {
		mode = (await json(req)).mode === 'old' ? 'old' : 'new';
		return send(res, 200, { mode });
	}
	if (req.method === 'GET' && p === '/internal/email/default-mjml') {
		if (!access.has(bearer(req))) return send(res, 401, { error: 'unauthorized' });
		if (mode === 'old') return send(res, 404, { error: 'not_found', message: 'not found' });
		const part = new URL(req.url, 'http://x').searchParams.get('part');
		if (part !== 'layout' && !defaults[part])
			return send(res, 400, { error: 'invalid_request', message: 'unknown part' });
		return send(res, 200, { mjml: defaultMJML(part) });
	}
	if (req.method === 'POST' && p.startsWith('/internal/email/')) {
		if (!access.has(bearer(req))) return send(res, 401, { error: 'unauthorized' });
		const b = await json(req);
		if (!defaults[b.template])
			return send(res, 400, { error: 'invalid_request', message: 'unknown template' });
		if (p === '/internal/email/test') return send(res, 200, { sent: true });
		if (p !== '/internal/email/preview') return send(res, 404, { error: 'not_found' });
		if (mode !== 'old' && /[<>]/.test(b.email?.footer ?? ''))
			return send(res, 400, {
				error: 'invalid_request',
				message: 'the footer is plain text; leave out < and >',
				field: 'email.footer'
			});
		// A little latency, like a real render.
		await new Promise((r) => setTimeout(r, 60));
		return send(res, 200, render(b));
	}
	send(res, 404, { error: 'not_found', message: 'not found' });
}).listen(INTERNAL, '127.0.0.1');

console.log(`fake auth: public :${PUBLIC}, internal :${INTERNAL}`);
