// A stand-in for telemetry.boson.chat: takes install reports and shows them.
//
//   node e2e/fake-telemetry.mjs     # :18697 (FAKE_TELEMETRY_PORT)
//   POST /v1/report  → 202 {"ok":true}   (POST /__status {"status":500} to refuse)
//   GET  /__reports  → every report so far
import { createServer } from 'node:http';
import process from 'node:process';

const PORT = Number(process.env.FAKE_TELEMETRY_PORT ?? 18697);
const reports = [];
let status = 202;

const send = (res, code, body) => {
	res.writeHead(code, { 'Content-Type': 'application/json' });
	res.end(JSON.stringify(body));
};
const read = (req) =>
	new Promise((ok) => {
		let b = '';
		req.on('data', (c) => (b += c));
		req.on('end', () => ok(b));
	});

createServer(async (req, res) => {
	const p = new URL(req.url, 'http://x').pathname;
	if (req.method === 'POST' && p === '/v1/report') {
		const body = await read(req);
		reports.push({ content_type: req.headers['content-type'], body: JSON.parse(body) });
		return send(res, status, status < 300 ? { ok: true } : { ok: false, error: 'invalid' });
	}
	if (req.method === 'GET' && p === '/__reports') return send(res, 200, { reports });
	if (req.method === 'POST' && p === '/__status') {
		status = JSON.parse(await read(req)).status ?? 202;
		return send(res, 200, { status });
	}
	send(res, 404, { error: 'not_found' });
}).listen(PORT, '127.0.0.1');
