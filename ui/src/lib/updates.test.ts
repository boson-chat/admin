import { describe, expect, it } from 'vitest';
import {
	impactWord,
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
