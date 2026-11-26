import { describe, expect, it } from 'vitest';
import {
	MODES,
	changes,
	cleanDomain,
	directoryProblem,
	domainMatches,
	domainProblem,
	homeSummary,
	quotaProblem,
	splitIdentity,
	sourceText,
	validate,
	type Draft,
	type Home
} from './federation';

const base: Draft = {
	mode: 'directory',
	allow: [],
	block: [],
	directory_url: 'https://directory.boson.chat',
	remote_upload_quota: '',
	remote_voice: true
};

describe('domains', () => {
	it('cleans what people paste', () => {
		expect(cleanDomain(' Boson.Chat ')).toBe('boson.chat');
		expect(cleanDomain('https://chat.example.org/path')).toBe('chat.example.org');
		expect(cleanDomain('alice@boson.chat')).toBe('boson.chat');
		expect(cleanDomain('boson.chat.')).toBe('boson.chat');
		expect(cleanDomain('*.Example.org')).toBe('*.example.org');
		expect(cleanDomain('*.*.example.org')).toBe('');
		expect(cleanDomain('localhost')).toBe('');
		expect(cleanDomain('203.0.113.9')).toBe('');
		expect(cleanDomain('not a domain')).toBe('');
	});
	it('says why one can’t be added', () => {
		expect(domainProblem('', [])).toBe('');
		expect(domainProblem('nope', [])).toMatch(/isn’t a domain/);
		expect(domainProblem('Boson.chat', ['boson.chat'])).toMatch(/already listed/);
		expect(domainProblem('boson.chat', [], { name: 'blocklist', list: ['boson.chat'] })).toMatch(/blocklist/);
		expect(domainProblem('boson.chat', [])).toBe('');
	});
});

describe('matching', () => {
	it('covers subdomains with *.', () => {
		expect(domainMatches('*.example.org', 'chat.example.org')).toBe(true);
		expect(domainMatches('*.example.org', 'example.org')).toBe(true);
		expect(domainMatches('*.example.org', 'badexample.org')).toBe(false);
		expect(domainMatches('boson.chat', 'x.boson.chat')).toBe(false);
	});
});

describe('validate', () => {
	it('needs someone on an allowlist', () => {
		expect(validate({ ...base, mode: 'allowlist' }).allow).toMatch(/at least one/);
		expect(validate({ ...base, mode: 'allowlist', allow: ['boson.chat'] })).toEqual({});
	});
	it('refuses a domain on both lists', () => {
		expect(validate({ ...base, allow: ['a.example'], block: ['a.example'] }).block).toMatch(/both/);
	});
	it('checks the directory only in directory mode', () => {
		expect(validate({ ...base, directory_url: 'http://x.example' }).directory_url).toMatch(/https/);
		expect(validate({ ...base, mode: 'open', directory_url: '' })).toEqual({});
		expect(directoryProblem('')).toMatch(/Enter/);
		expect(directoryProblem('https://dir.example.org')).toBe('');
	});
	it('checks the quota', () => {
		expect(quotaProblem('')).toBe('');
		expect(quotaProblem('2 GB')).toBe('');
		expect(quotaProblem('500mib')).toBe('');
		expect(quotaProblem('lots')).toMatch(/size like/);
	});
});

describe('changes', () => {
	it('only sends what changed', () => {
		expect(changes(base, base)).toEqual({});
		expect(
			changes(
				{ ...base, mode: 'open', block: ['x.example'], remote_upload_quota: ' 1GB ', remote_voice: false },
				base
			)
		).toEqual({ mode: 'open', block: ['x.example'], remote_upload_quota: '1GB', remote_voice: false });
	});
});

describe('words', () => {
	it('explains every mode with its risk', () => {
		expect(MODES.map((m) => m.value)).toEqual(['off', 'allowlist', 'directory', 'open']);
		for (const m of MODES) expect(m.risk.length).toBeGreaterThan(20);
		expect(MODES.find((m) => m.value === 'directory')?.note).toBe('Default');
	});
	it('names sources', () => {
		expect(sourceText('env', 'FEDERATION_MODE')).toContain('FEDERATION_MODE');
		expect(sourceText('file', '')).toContain('boson.yaml');
		expect(sourceText(undefined, '')).toBe('Built-in default');
	});
	it('summarises the home status', () => {
		const h = (state: Home['state'], ok = true): Home => ({
			state,
			domain: 'example.org',
			checks: [{ id: 'accepts', label: 'x', ok, warn: !ok }],
			checked_at: ''
		});
		expect(homeSummary(h('published')).tone).toBe('ok');
		expect(homeSummary(h('published')).text).toContain('user@example.org');
		expect(homeSummary(h('published', false)).tone).toBe('warn');
		expect(homeSummary(h('missing')).tone).toBe('bad');
		expect(homeSummary(h('off')).tone).toBe('off');
		expect(homeSummary(undefined).title).toBe('Unknown');
	});
	it('splits identities', () => {
		expect(splitIdentity('alice@boson.chat')).toEqual(['alice', 'boson.chat']);
		expect(splitIdentity('weird')).toEqual(['weird', '']);
	});
});
