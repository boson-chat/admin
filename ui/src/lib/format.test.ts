import { describe, expect, it } from 'vitest';
import { ago, signInMethod } from './format';

describe('ago', () => {
	const now = Date.parse('2026-11-16T12:00:00Z');
	const at = (secs: number) => new Date(now - secs * 1000).toISOString();
	it('is compact', () => {
		expect(ago(at(5), now)).toBe('just now');
		expect(ago(at(12 * 60), now)).toBe('12m ago');
		expect(ago(at(3 * 3600 + 59 * 60), now)).toBe('3h ago');
		expect(ago(at(5 * 86400), now)).toBe('5d ago');
	});
	it('falls back to the date after a month', () => {
		expect(ago('2026-03-02T10:00:00Z', now)).toBe('Mar 2, 2026');
	});
	it('handles missing values and clock skew', () => {
		expect(ago(null, now)).toBe('');
		expect(ago(at(-30), now)).toBe('just now');
	});
});

describe('signInMethod', () => {
	it('names methods', () => {
		expect(signInMethod('password')).toBe('Password');
		expect(signInMethod('oidc:corp')).toBe('corp (OIDC)');
		expect(signInMethod('saml:okta')).toBe('okta (SAML)');
		expect(signInMethod(null)).toBe('');
	});
});
