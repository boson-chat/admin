import { describe, expect, it } from 'vitest';
import { EMAIL_TEMPLATES, bodyKey, emailOk, insertAt, subjectKey, unknownVars } from './emails';

describe('emails', () => {
	it('lists the four templates with the contract’s variables', () => {
		expect(EMAIL_TEMPLATES.map((t) => t.id)).toEqual([
			'verification',
			'password_reset',
			'account_setup',
			'invite'
		]);
		expect(EMAIL_TEMPLATES.find((t) => t.id === 'invite')?.vars).toContain('inviter');
		expect(subjectKey('invite')).toBe('auth.email.templates.invite.subject');
		expect(bodyKey('invite')).toBe('auth.email.templates.invite.body');
	});
	it('finds variables a template doesn’t fill', () => {
		expect(unknownVars('Hi {{username}}, {{ inviter }} and {{nope}} {{nope}}', ['username', 'link'])).toEqual(
			['inviter', 'nope']
		);
		expect(unknownVars('{{network}}', ['network'])).toEqual([]);
	});
	it('inserts at the caret or over the selection', () => {
		expect(insertAt('Hello !', 6, 6, '{{username}}')).toEqual({ value: 'Hello {{username}}!', caret: 18 });
		expect(insertAt('Hello XX!', 6, 8, '{{a}}')).toEqual({ value: 'Hello {{a}}!', caret: 11 });
		expect(insertAt('abc', 99, 99, 'd')).toEqual({ value: 'abcd', caret: 4 });
	});
	it('checks one address', () => {
		expect(emailOk(' me@example.com ')).toBe(true);
		for (const bad of ['', 'me', 'a@b', 'a@b.c, d@e.f', 'Me <me@example.com>'])
			expect(emailOk(bad)).toBe(false);
	});
});
