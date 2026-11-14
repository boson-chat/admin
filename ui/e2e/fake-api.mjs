// A stand-in for the api service's internal listener, for the Nick
// registration page's browser checks:
//
//   GET  /internal/config/status       the stack's config status contract
//   GET  /internal/nick-claims/status  X-Internal-Token
//   POST /internal/nick-claims/test    X-Internal-Token
//   POST /__state {...}                 change what status answers (spec only)
//
//   FAKE_API_CONFIG=/path/boson.yaml node e2e/fake-api.mjs   # :18682, token "e2e-api"
//
// It reads nick_claim: from the config file (a few regexes, enough for
// what the panel writes), so a save shows up in its status like the real
// api's reload would.
import { createServer } from 'node:http';
import { readFileSync, statSync } from 'node:fs';
import process from 'node:process';

const PORT = Number(process.env.FAKE_API_PORT ?? 18682);
const TOKEN = process.env.FAKE_API_TOKEN ?? 'e2e-api';
const CONFIG = process.env.FAKE_API_CONFIG ?? '';
const CURRENT_HOST = 'pop.env.example.com';
const CURRENT_PASSWORD = 'mailbox-password';

// What the env would give (defaults the file overrides).
const env = { email_domain: '', host: CURRENT_HOST, port: 995, tls: true, user: 'reg@env.example.com' };
let state = {
	password_set: true,
	running: null, // null: follow enabled
	last_error: '',
	consecutive_failures: 0,
	pending_claims: 2,
	token_ok: true
};

function section() {
	if (!CONFIG) return {};
	let text = '';
	try {
		text = readFileSync(CONFIG, 'utf8');
	} catch {
		return {};
	}
	const m = text.match(/^nick_claim:\n((?:[ \t]+.*\n?|\n)*)/m);
	if (!m) return {};
	const body = m[1];
	const get = (re) =>
		body
			.match(re)?.[1]
			?.trim()
			.replace(/^["']|["']$/g, '');
	const out = {};
	const enabled = get(/^ {2}enabled:\s*(\S+)/m);
	if (enabled !== undefined) out.enabled = enabled === 'true';
	const domain = get(/^ {2}email_domain:\s*(.+)$/m);
	if (domain) out.email_domain = domain;
	const host = get(/^ {4}host:\s*(.+)$/m);
	if (host) out.host = host;
	const port = get(/^ {4}port:\s*(\d+)/m);
	if (port) out.port = Number(port);
	const tls = get(/^ {4}tls:\s*(\S+)/m);
	if (tls !== undefined) out.tls = tls === 'true';
	const user = get(/^ {4}user:\s*(.+)$/m);
	if (user) out.user = user;
	return out;
}

function status() {
	const s = { enabled: false, ...env, ...section() };
	const running = state.running ?? s.enabled;
	const now = new Date();
	const ago = (sec) => new Date(now.getTime() - sec * 1000).toISOString();
	return {
		enabled: s.enabled,
		email_domain: s.email_domain,
		inbound: { host: s.host, port: s.port, tls: s.tls, user: s.user, password_set: state.password_set },
		worker: {
			running,
			last_drain_at: running ? ago(4) : null,
			last_ok_at: running ? ago(state.consecutive_failures ? 380 : 4) : null,
			last_error: state.last_error,
			consecutive_failures: state.consecutive_failures
		},
		pending_claims: state.pending_claims
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

function send(res, code, body) {
	res.writeHead(code, { 'Content-Type': 'application/json' });
	res.end(JSON.stringify(body));
}

const startedAt = new Date().toISOString();

createServer(async (req, res) => {
	const p = new URL(req.url, 'http://x').pathname;
	if (req.method === 'GET' && p === '/healthz') return send(res, 200, { status: 'ok' });
	if (req.method === 'POST' && p === '/__state') {
		const b = await json(req);
		state = b.reset
			? {
					password_set: true,
					running: null,
					last_error: '',
					consecutive_failures: 0,
					pending_claims: 2,
					token_ok: true
				}
			: { ...state, ...b };
		return send(res, 200, state);
	}
	if (req.method === 'GET' && p === '/internal/config/status') {
		let mtime = null;
		try {
			mtime = statSync(CONFIG).mtime.toISOString();
		} catch {
			/* no file yet */
		}
		return send(res, 200, {
			path: '/config/boson.yaml',
			loaded_at: new Date().toISOString(),
			file_mtime: mtime,
			ok: true,
			restart_required: [],
			started_at: startedAt
		});
	}
	if (p.startsWith('/internal/nick-claims/')) {
		if (!state.token_ok || req.headers['x-internal-token'] !== TOKEN)
			return send(res, 401, { error: 'unauthorized' });
		if (req.method === 'GET' && p === '/internal/nick-claims/status') return send(res, 200, status());
		if (req.method === 'POST' && p === '/internal/nick-claims/test') {
			const cur = status().inbound;
			const b = (await json(req)).inbound ?? {};
			const host = b.host ?? cur.host;
			const port = b.port ?? cur.port;
			const user = b.user ?? cur.user;
			// The stored password is only reused for the server in use.
			const password = b.password ?? (host === CURRENT_HOST ? CURRENT_PASSWORD : '');
			await new Promise((r) => setTimeout(r, 150));
			if (host.startsWith('unreachable'))
				return send(res, 200, {
					ok: false,
					message: `connect failed: dial tcp ${host}:${port}: i/o timeout`,
					messages: 0
				});
			if (password !== CURRENT_PASSWORD)
				return send(res, 200, {
					ok: false,
					message: 'sign-in failed: -ERR [AUTH] invalid login',
					messages: 0
				});
			return send(res, 200, {
				ok: true,
				message: `Connected to ${host}:${port} and signed in as ${user}; 3 message(s) waiting.`,
				messages: 3
			});
		}
	}
	send(res, 404, { error: 'not_found' });
}).listen(PORT, '127.0.0.1');

console.log(`fake api: internal :${PORT}`);
