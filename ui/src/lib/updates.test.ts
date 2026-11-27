import { describe, expect, it } from 'vitest';
import {
	checkError,
	checkPending,
	checkPhase,
	diffClass,
	impactWord,
	localEdits,
	shortImage,
	type StackStatus,
	renderMarkdown,
	withoutTitle,
	updatesByService,
	type Impact,
	type UpdatesStatus
} from './updates';

const semrel = `## [1.22.0](https://github.com/boson-chat/auth/compare/v1.21.1...v1.22.0) (2026-11-24)

### ⚠ BREAKING CHANGES

* \`AUTH_KEY\` is required

### Features

* send users back to the app ([#26](https://github.com/boson-chat/auth/issues/26)) ([da1190f](https://github.com/boson-chat/auth/commit/da1190f))
* **sso:** SAML`;

describe('renderMarkdown', () => {
	it('renders a semantic-release changelog', () => {
		const html = renderMarkdown(semrel);
		expect(html).toContain('<h4><a href="https://github.com/boson-chat/auth/compare/v1.21.1...v1.22.0"');
		expect(html).toContain('<h5>⚠ BREAKING CHANGES</h5>');
		expect(html).toContain('<li><code>AUTH_KEY</code> is required</li>');
		expect(html).toContain('<strong>sso:</strong> SAML');
		expect(html.match(/<ul>/g)?.length).toBe(2);
	});

	it('never lets markup through', () => {
		const html = renderMarkdown(
			'<script>alert(1)</script>\n\n* <img src=x onerror=alert(1)>\n\n[x](javascript:alert(1)) [y](data:text/html,hi) `<b>`'
		);
		expect(html).not.toMatch(/<script|<img|javascript:|data:text/);
		expect(html).toContain('&lt;script&gt;');
		expect(html).toContain('<code>&lt;b&gt;</code>');
		const div = document.createElement('div');
		div.innerHTML = html;
		expect(div.querySelectorAll('script,img,[onerror]').length).toBe(0);
		expect(div.querySelectorAll('a').length).toBe(0);
	});

	it('keeps quotes out of attributes', () => {
		const html = renderMarkdown('[a](https://example.com/"onmouseover="x)');
		const div = document.createElement('div');
		div.innerHTML = html;
		const a = div.querySelector('a');
		expect(a?.getAttribute('onmouseover')).toBeNull();
		expect(a?.getAttribute('rel')).toBe('noopener noreferrer');
	});

	it('handles fences, ordered lists and empty bodies', () => {
		expect(renderMarkdown('')).toBe('');
		const html = renderMarkdown('1. one\n2. two\n\n```\n<raw>\n```');
		expect(html).toContain('<ol>\n<li>one</li>\n<li>two</li>\n</ol>');
		expect(html).toContain('<pre><code>&lt;raw&gt;</code></pre>');
	});
});

const imp = (o: Partial<Impact>): Impact => ({
	restart: true,
	disruptive: false,
	chat_disconnect: false,
	drops_calls: false,
	downtime_seconds: 5,
	who: '',
	...o
});

describe('impact and stack mapping', () => {
	it('words the impact', () => {
		expect(impactWord(imp({ chat_disconnect: true, disruptive: true })).text).toBe('disconnects chat');
		expect(impactWord(imp({ drops_calls: true, disruptive: true })).text).toBe('drops calls');
		expect(impactWord(imp({ disruptive: true })).text).toBe('interrupts users');
		expect(impactWord(imp({})).text).toBe('brief pause');
		expect(impactWord(imp({ restart: false })).text).toBe('no restart');
	});
	it('maps components to Stack page names', () => {
		const st = {
			components: [
				{ id: 'voice', update: true, latest: '1.16.0' },
				{ id: 'auth', update: true, latest: '1.22.0' },
				{ id: 'web', update: false, latest: '0.1.0' }
			]
		} as unknown as UpdatesStatus;
		expect(updatesByService(st)).toEqual({ quicvoiced: '1.16.0', auth: '1.22.0' });
	});
});

describe('withoutTitle', () => {
	it('drops a heading that only repeats the version', () => {
		expect(withoutTitle(semrel, '1.22.0')).not.toContain('## [1.22.0]');
		expect(withoutTitle(semrel, '1.22.0')).toContain('### Features');
		expect(withoutTitle('## 1.22.0 Big rename\n\n* x', '1.22.0')).toContain('Big rename');
		expect(withoutTitle('* x', '1.22.0')).toBe('* x');
	});
});

describe('checkPhase', () => {
	const base = { checking: false, available: 0, components: [], history: [] } as unknown as UpdatesStatus;
	const comp = { id: 'auth', update: false } as unknown as UpdatesStatus['components'][number];
	it('never says "done" before a check finished', () => {
		expect(checkPhase(null)).toBe('waiting');
		expect(checkPhase(base)).toBe('waiting');
		expect(checkPhase(base, true)).toBe('checking');
		expect(checkPhase({ ...base, checking: true })).toBe('checking');
		expect(checkPending(base)).toBe(true);
		expect(checkPending({ ...base, checking: true })).toBe(true);
		expect(checkPending(null)).toBe(false);
	});
	it('done once a check finished, even while the next one runs', () => {
		const done = { ...base, checked_at: '2026-11-26T12:00:00Z', components: [comp] };
		expect(checkPhase(done)).toBe('done');
		expect(checkPending(done)).toBe(false);
		expect(checkPhase({ ...done, checking: true })).toBe('done');
		expect(checkPending({ ...done, checking: true })).toBe(true);
	});
	it('failed when an error means "nothing to update" would be a guess', () => {
		const failed = { ...base, checked_at: '2026-11-26T12:00:00Z', error: 'docker: connection refused' };
		expect(checkPhase(failed)).toBe('failed');
		expect(checkError(failed)).toBe('docker: connection refused');
		expect(checkPending(failed)).toBe(false);
		expect(checkPhase({ ...failed, components: [comp] })).toBe('failed');
		// Updates it did find are shown (with the warning).
		expect(checkPhase({ ...failed, components: [{ ...comp, update: true }] })).toBe('done');
		// Every installed part errored (GitHub unreachable): not "up to date".
		const gh = {
			...base,
			checked_at: '2026-11-26T12:00:00Z',
			components: [
				{ ...comp, present: true, error: 'github: timeout' },
				{ ...comp, id: 'evrd', present: false }
			]
		};
		expect(checkPhase(gh)).toBe('failed');
		expect(checkError(gh)).toBe('github: timeout');
		const partial = { ...gh, components: [...gh.components, { ...comp, id: 'web', present: true }] };
		expect(checkPhase(partial)).toBe('done');
	});
});

describe('stack release helpers', () => {
	it('shortens image references', () => {
		expect(shortImage('harbor.floret.dev/boson/stack-ergo:1.2.0')).toBe('stack-ergo:1.2.0');
		expect(shortImage('harbor.floret.dev/boson/auth:1.20.0@sha256:abc')).toBe('auth:1.20.0');
		expect(shortImage('postgres:18-alpine')).toBe('postgres:18-alpine');
		expect(shortImage(undefined)).toBe('—');
	});
	it('colours diff lines', () => {
		expect(diffClass('--- a/compose.yaml')).toBe('dh');
		expect(diffClass('+++ b/compose.yaml')).toBe('dh');
		expect(diffClass('@@ -1,3 +1,3 @@')).toBe('dhunk');
		expect(diffClass('+    image: traefik:v3.7.20')).toBe('dadd');
		expect(diffClass('-    image: traefik:v3.7.19')).toBe('ddel');
		expect(diffClass('  context')).toBe('');
	});
	it('lists the files edited on this server', () => {
		const s = {
			files: [
				{ path: 'compose.yaml', action: 'update' },
				{ path: 'traefik/dynamic/stack.yml', action: 'local' },
				{ path: 'docs/old.md', action: 'keep' },
				{ path: 'README.md', action: 'unknown' }
			]
		} as StackStatus;
		expect(localEdits(s).map((f) => f.path)).toEqual(['traefik/dynamic/stack.yml', 'docs/old.md']);
		expect(localEdits(undefined)).toEqual([]);
	});
});
