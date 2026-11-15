// A stand-in for the files service's internal listener, for the Uploads
// and Uploaded files pages' browser checks:
//
//   GET    /internal/config/status   the stack's config status contract
//   GET    /internal/files/status    X-Internal-Token
//   GET    /internal/files           ?user=&q=&cursor=
//   DELETE /internal/files/{id}
//   GET    /internal/files/usage     ?user=
//   POST   /__state {...}            change what it answers (spec only)
//
//   FAKE_FILES_CONFIG=/path/boson.yaml node e2e/fake-files.mjs   # :18683, token "e2e-files"
//
// It reads files: from the config file (a few regexes, enough for what the
// panel writes) and echoes it in its status like the real service would.
import { createServer } from 'node:http';
import { readFileSync, statSync } from 'node:fs';
import process from 'node:process';

const PORT = Number(process.env.FAKE_FILES_PORT ?? 18683);
const TOKEN = process.env.FAKE_FILES_TOKEN ?? 'e2e-files';
const CONFIG = process.env.FAKE_FILES_CONFIG ?? '';
const GB = 2 ** 30;
const MB = 2 ** 20;

const fresh = () => ({ token_ok: true, storage_ok: true, deleted: [] });
let state = fresh();

const units = { B: 1, KB: 2 ** 10, MB, GB, TB: 2 ** 40 };
const size = (s) => {
	const m = /^(\d+)(B|KB|MB|GB|TB)$/.exec(s ?? '');
	return m ? Number(m[1]) * units[m[2]] : undefined;
};
const dur = (s) => {
	const m = /^(\d+)(h|m|s)$/.exec(s ?? '');
	return m ? Number(m[1]) * { h: 3600, m: 60, s: 1 }[m[2]] : undefined;
};

function section() {
	if (!CONFIG) return '';
	try {
		return readFileSync(CONFIG, 'utf8').match(/^files:\n((?:[ \t]+.*\n?|\n)*)/m)?.[1] ?? '';
	} catch {
		return '';
	}
}

function config() {
	const body = section();
	const block = (name) => body.match(new RegExp(`^ {2}${name}:\\n((?: {4,}.*\\n?)*)`, 'm'))?.[1] ?? '';
	const get = (text, re) => text.match(re)?.[1]?.trim();
	const acc = block('account');
	const guest = block('guest');
	const enabled = get(body, /^ {2}enabled:\s*(\S+)/m);
	const gEnabled = get(guest, /^ {4}enabled:\s*(\S+)/m);
	const window = dur(get(body, /^ {2}window:\s*(\S+)/m)) ?? 259200;
	return {
		enabled: enabled === 'true',
		guest_enabled: gEnabled === undefined ? true : gEnabled === 'true',
		limits: {
			account: {
				quota_bytes: size(get(acc, /^ {4}quota:\s*(\S+)/m)) ?? 5 * GB,
				window_seconds: window,
				max_file_bytes: size(get(acc, /^ {4}max_file:\s*(\S+)/m)) ?? 100 * MB
			},
			guest: {
				quota_bytes: size(get(guest, /^ {4}quota:\s*(\S+)/m)) ?? 500 * MB,
				window_seconds: window,
				max_file_bytes: size(get(guest, /^ {4}max_file:\s*(\S+)/m)) ?? 25 * MB
			}
		},
		retention_seconds: dur(get(body, /^ {2}retention:\s*(\S+)/m)) ?? 259200,
		driver: get(body, /^ {4}driver:\s*(\S+)/m) ?? 'local',
		bucket: get(body, /^ {6}bucket:\s*(\S+)/m) ?? ''
	};
}

const now = Date.now();
const at = (h) => new Date(now + h * 3600e3).toISOString();
const FILES = [
	[
		'k3vQ9xT2mLp8wRf4Zb7nYc',
		'sunset-over-the-bay.jpg',
		2.4 * MB,
		'image/jpeg',
		'usr_admin',
		-2,
		{ width: 4032, height: 3024 }
	],
	['Hq2Lr8VnX4cT9pWz1Mb6Kd', 'meeting-notes.pdf', 312 * 1024, 'application/pdf', 'usr_admin', -20],
	['Ap7Ys3Kd9Qw2Ex5Rt8Vm1N', 'clip.mp4', 48 * MB, 'video/mp4', 'guest', -5],
	['Zt4Nc8Bv1Lx6Qm9Wp3Rk2S', 'build-logs-2026-11-13.txt', 18 * 1024, 'text/plain', 'usr_admin', -40],
	['Pw9Er2Ty5Ui8Op1As4Df7G', 'diagram.svg', 41 * 1024, 'image/svg+xml', 'guest', -60]
].map(([id, name, sz, ct, owner, h, dims]) => ({
	id,
	url: `https://files.example.com/f/${id}/${encodeURIComponent(name)}`,
	name,
	size: Math.round(sz),
	content_type: ct,
	created_at: at(h),
	expires_at: at(h + 72),
	...(dims ?? {}),
	owner:
		owner === 'guest'
			? { kind: 'guest', id: 'gst_' + id.slice(0, 6), ip_hash: '3f9a…c21e' }
			: { kind: 'account', id: owner }
}));

function list(q) {
	let out = FILES.map((f) =>
		state.deleted.includes(f.id) ? { ...f, deleted_at: new Date().toISOString() } : f
	);
	const user = q.get('user');
	if (user) out = out.filter((f) => f.owner.id === user);
	const term = (q.get('q') ?? '').toLowerCase();
	if (term) out = out.filter((f) => f.name.toLowerCase().includes(term));
	return { files: out };
}

function send(res, code, body) {
	res.writeHead(code, { 'Content-Type': 'application/json' });
	res.end(body === undefined ? '' : JSON.stringify(body));
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

const startedAt = new Date().toISOString();

createServer(async (req, res) => {
	const u = new URL(req.url, 'http://x');
	const p = u.pathname;
	if (req.method === 'POST' && p === '/__state') {
		const b = await json(req);
		state = b.reset ? fresh() : { ...state, ...b };
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
	if (!p.startsWith('/internal/files')) return send(res, 404, { error: 'not_found' });
	if (!state.token_ok || req.headers['x-internal-token'] !== TOKEN)
		return send(res, 401, { error: 'unauthorized' });
	if (req.method === 'GET' && p === '/internal/files/status') {
		const c = config();
		const live = FILES.filter((f) => !state.deleted.includes(f.id));
		const s3 = c.driver === 's3';
		return send(res, 200, {
			version: '0.1.0',
			storage: {
				driver: c.driver,
				ok: state.storage_ok,
				...(state.storage_ok ? {} : { error: 'write test failed: no space left on device' }),
				...(s3
					? { bucket: c.bucket, endpoint: 'http://minio:9000' }
					: { path: '/data', free_bytes: Math.round(19.2 * GB), total_bytes: 24 * GB })
			},
			totals: {
				files: live.length,
				bytes: live.reduce((n, f) => n + f.size, 0),
				window_uploads: FILES.length,
				window_bytes: FILES.reduce((n, f) => n + f.size, 0),
				uploaders: 3
			},
			config: {
				enabled: c.enabled,
				guest_enabled: c.guest_enabled,
				limits: c.limits,
				retention_seconds: c.retention_seconds
			},
			errors: state.storage_ok ? [] : ['uploads are refused until storage works again']
		});
	}
	if (req.method === 'GET' && p === '/internal/files/usage') {
		const c = config();
		const mine = FILES.filter((f) => f.owner.id === u.searchParams.get('user'));
		return send(res, 200, {
			used: Math.round(1.2 * GB) + mine.reduce((n, f) => n + f.size, 0),
			quota: c.limits.account.quota_bytes,
			window_seconds: c.limits.account.window_seconds,
			max_file: c.limits.account.max_file_bytes,
			frees: [
				{ bytes: GB, at: at(20) },
				{ bytes: 200 * MB, at: at(52) }
			],
			files: mine.filter((f) => !state.deleted.includes(f.id)).length
		});
	}
	if (req.method === 'GET' && p === '/internal/files') return send(res, 200, list(u.searchParams));
	const m = /^\/internal\/files\/([A-Za-z0-9]+)$/.exec(p);
	if (req.method === 'DELETE' && m) {
		if (!FILES.some((f) => f.id === m[1]) || state.deleted.includes(m[1]))
			return send(res, 404, { error: 'not_found', message: 'no such file' });
		state.deleted.push(m[1]);
		return send(res, 204);
	}
	send(res, 404, { error: 'not_found' });
}).listen(PORT, '127.0.0.1');

console.log(`fake files: internal :${PORT}`);
