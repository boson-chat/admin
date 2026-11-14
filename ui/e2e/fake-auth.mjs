// A stand-in auth service for the Emails page's browser checks: enough of
// the public API to sign in (one legacy admin), and an internal listener
// whose email preview echoes what it's sent into an MJML-like layout (with
// a dark-mode media query), so the spec can see unsaved settings arrive.
//
//   node e2e/fake-auth.mjs            # public :18680, internal :18681
//   POST <internal>/__mode {"mode":"old"}  # act like auth 1.10.x: ignore
//                                          # "email", send no from
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
const access = new Set();
const refresh = new Set();
let n = 0;
let mode = 'new';
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
		return send(res, 200, { user });
	}
	if (req.method === 'GET' && p === '/v1/config')
		return send(res, 200, { registration: 'invite', require_email_verification: true, sso_providers: [] });
	send(res, 404, { error: 'not_found', message: 'not found' });
}).listen(PUBLIC, '127.0.0.1');

createServer(async (req, res) => {
	const p = new URL(req.url, 'http://x').pathname;
	if (p === '/healthz' || p === '/readyz') return send(res, 200, { status: 'ok' });
	if (req.method === 'POST' && p === '/__mode') {
		mode = (await json(req)).mode === 'old' ? 'old' : 'new';
		return send(res, 200, { mode });
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
