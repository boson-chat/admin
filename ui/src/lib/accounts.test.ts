import { describe, expect, it } from 'vitest';
import { emailOffFor, emailOffEffects, signupMessageProblem, MAX_SIGNUP_MESSAGE } from './accounts';

describe('email off', () => {
	it('spells out what local accounts lose', () => {
		const text = emailOffFor('local').join(' ');
		expect(text).toMatch(/verification/);
		expect(text).toMatch(/password reset/);
		expect(text).toMatch(/share it yourself/);
		expect(emailOffFor('local')).toBe(emailOffEffects);
	});
	it('says NickServ accounts are unaffected', () => {
		expect(emailOffFor('nickserv')).toHaveLength(1);
		expect(emailOffFor('nickserv')[0]).toMatch(/NickServ/);
	});
});

describe('signupMessageProblem', () => {
	it('accepts empty and one-line messages', () => {
		expect(signupMessageProblem('')).toBe('');
		expect(signupMessageProblem('Register at https://example.org')).toBe('');
		expect(signupMessageProblem('x'.repeat(MAX_SIGNUP_MESSAGE))).toBe('');
	});
	it('refuses line breaks and long messages', () => {
		expect(signupMessageProblem('a\nb')).toMatch(/one line/);
		expect(signupMessageProblem('x'.repeat(MAX_SIGNUP_MESSAGE + 1))).toMatch(/500/);
	});
});
