import { describe, expect, it } from 'vitest';
import type { ConfigKey, ConfigView } from './types';
import {
	KEYS,
	NEEDED_TO_ENABLE,
	changeCount,
	exampleAddress,
	changes,
	draftFrom,
	otherServer,
	passwordState,
	testBody,
	validate,
	workerState,
	type Draft,
	type NickClaimStatus
} from './nickclaim';

function viewWith(values: Record<string, unknown>): ConfigView {
	const keys = Object.entries(values).map(
		([key, value]) =>
			({
				key,
				kind: 'string',
				perm: 'settings.manage',
				services: ['api'],
				set: true,
				value,
				editable: true
			}) as ConfigKey
	);
	return { enabled: true, exists: true, issues: [], keys };
}

const status = (over: Partial<NickClaimStatus> = {}): NickClaimStatus => ({
	enabled: false,
	email_domain: 'env.example.com',
	inbound: { host: 'pop.env.example.com', port: 995, tls: true, user: 'reg@env.example.com' },
	worker: { running: false, last_drain_at: null, last_ok_at: null, last_error: '', consecutive_failures: 0 },
	pending_claims: 0,
	...over
});

const blank: Draft = {
	enabled: false,
	email_domain: '',
	rate_limit_per_hour: null,
	host: '',
	port: 995,
	tls: true,
	user: ''
};

describe('draftFrom', () => {
	it('prefers the file, then the api (env), then defaults', () => {
		const { draft, sources } = draftFrom(
			viewWith({ [KEYS.email_domain]: 'nick.example.com', [KEYS.rate_limit_per_hour]: 9 }),
			status()
		);
		expect(draft.email_domain).toBe('nick.example.com');
		expect(sources.email_domain).toBe('file');
		expect(draft.rate_limit_per_hour).toBe(9);
		expect(draft.host).toBe('pop.env.example.com');
		expect(sources.host).toBe('env');
		expect(draft.enabled).toBe(false);
	});
	it('falls back to defaults without the api', () => {
		const { draft, sources } = draftFrom(viewWith({}), null);
		expect(draft).toEqual(blank);
		expect(sources.port).toBe('default');
	});
});

describe('validate', () => {
	it('blocks enabling without a domain or a mail server', () => {
		const e = validate({ ...blank, enabled: true });
		expect(e.email_domain).toBe(NEEDED_TO_ENABLE);
		expect(e.host).toBe(NEEDED_TO_ENABLE);
		expect(validate({ ...blank, enabled: true, email_domain: 'n.example.com', host: 'pop' })).toEqual({});
	});
	it('checks each field', () => {
		const e = validate({
			...blank,
			email_domain: '10.0.0.1',
			host: 'bad host!',
			port: 70000,
			rate_limit_per_hour: 0,
			user: 'x'.repeat(255)
		});
		expect(Object.keys(e).sort()).toEqual(['email_domain', 'host', 'port', 'rate_limit_per_hour', 'user']);
		expect(validate({ ...blank, port: null }).port).toMatch(/Enter a port/);
		expect(validate({ ...blank, rate_limit_per_hour: null })).toEqual({});
		expect(validate({ ...blank, host: '192.0.2.10' })).toEqual({});
	});
});

describe('changes', () => {
	it('sends only what changed, clears to unset and adds the protocol', () => {
		const saved = { ...blank, host: 'pop.env.example.com', user: 'old@example.com' };
		const c = changes(
			{ ...saved, enabled: true, email_domain: ' Nick.Example.com. ', host: 'pop.example.com', user: '' },
			saved,
			viewWith({})
		);
		expect(c.set).toEqual({
			[KEYS.enabled]: true,
			[KEYS.email_domain]: 'nick.example.com',
			[KEYS.host]: 'pop.example.com',
			'nick_claim.inbound.protocol': 'pop3'
		});
		expect(c.unset).toEqual([KEYS.user]);
		expect(changeCount(c)).toBe(4);
		expect(changes(saved, saved, null)).toEqual({});
		// An emptied number input is a reset, not a value.
		const r = changes({ ...saved, rate_limit_per_hour: NaN }, { ...saved, rate_limit_per_hour: 7 }, null);
		expect(r.unset).toEqual([KEYS.rate_limit_per_hour]);
	});
	it('leaves an existing protocol alone', () => {
		const c = changes({ ...blank, port: 110 }, blank, viewWith({ 'nick_claim.inbound.protocol': 'pop3' }));
		expect(c.set).toEqual({ [KEYS.port]: 110 });
	});
});

describe('test request', () => {
	it('sends the form values and a typed password only when given', () => {
		const d = { ...blank, host: ' POP.example.com ', port: 110, tls: false, user: 'reg@example.com' };
		expect(testBody(d, '')).toEqual({
			inbound: { protocol: 'pop3', host: 'pop.example.com', port: 110, tls: false, user: 'reg@example.com' }
		});
		expect(testBody(d, 'pw').inbound.password).toBe('pw');
	});
	it('knows when the api won’t reuse its password', () => {
		expect(otherServer({ ...blank, host: 'pop.env.example.com' }, status())).toBe(false);
		expect(otherServer({ ...blank, host: 'pop.other.com' }, status())).toBe(true);
		expect(otherServer({ ...blank, host: '' }, status())).toBe(false);
	});
});

describe('status words', () => {
	it('password', () => {
		expect(passwordState(status({ inbound: { ...status().inbound, password_set: true } }))).toBe('set');
		expect(passwordState(status({ inbound: { ...status().inbound, password_set: false } }))).toBe('missing');
		expect(passwordState(status())).toBe('unknown');
		expect(passwordState(null)).toBe('unknown');
	});
	it('worker', () => {
		expect(workerState(status())).toBe('off');
		expect(workerState(status({ enabled: true, worker: { running: true } }))).toBe('running');
		expect(
			workerState(
				status({ enabled: true, worker: { running: true, consecutive_failures: 2, last_error: 'x' } })
			)
		).toBe('failing');
		expect(workerState(status({ enabled: true, worker: { running: false } }))).toBe('stopped');
		expect(workerState(status({ enabled: true, worker: { running: false }, config_error: 'no host' }))).toBe(
			'failing'
		);
	});
});

describe('exampleAddress', () => {
	it('shows the plus-addressed pattern on the domain', () => {
		expect(exampleAddress(' Nick.Example.com ')).toBe('reg+<user id>-<token>@nick.example.com');
		expect(exampleAddress('')).toBe('reg+<user id>-<token>@example.com');
	});
});
