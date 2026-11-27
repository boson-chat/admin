import { api } from './api';

/** GET /api/auth/accounts: what auth's accounts can do on this network. */
export interface AuthAccounts {
	/** auth can send email (SMTP is set up). Older auth doesn't say: true. */
	email_configured: boolean;
	/** auth reported its mailer (newer auth). */
	email_reported: boolean;
	/** local: auth's own accounts; nickserv: the IRC network's NickServ accounts. */
	account_backend: 'local' | 'nickserv' | string;
	/** The email verification sign-in actually enforces. */
	require_email_verification: boolean;
	password_reset: boolean;
	signup: boolean;
	signup_message: string | null;
}

export const NICKSERV_REGISTRATION_KEY = 'auth.nickserv.registration';
export const NICKSERV_SIGNUP_MESSAGE_KEY = 'auth.nickserv.signup_message';
/** NickServ sign-up messages are one line, at most this long (the stack config key's limit). */
export const MAX_SIGNUP_MESSAGE = 500;

/** Loads /api/auth/accounts; null when auth can't be reached (the page carries on). */
export async function loadAuthAccounts(): Promise<AuthAccounts | null> {
	try {
		return await api<AuthAccounts>('GET', '/auth/accounts');
	} catch {
		return null;
	}
}

/** What running without email means, one line each (local accounts). */
export const emailOffEffects = [
	'No email verification: new accounts can sign in straight away, whatever “Require a verified email” says.',
	'No password reset by email: “Forgot password” is off in the apps.',
	'No emailed invites or set-password links: the panel shows the link and you share it yourself.'
];

/** The email-off effects for an account backend: NickServ handles its own email and passwords. */
export function emailOffFor(backend: string): string[] {
	if (backend === 'nickserv')
		return [
			'Boson sends no email. NickServ accounts verify and reset passwords on IRC, so sign-up and sign-in aren’t affected.'
		];
	return emailOffEffects;
}

/** A problem with a NickServ sign-up message, or ''. */
export function signupMessageProblem(msg: string): string {
	if (/[\r\n]/.test(msg)) return 'Keep the message on one line.';
	if ([...msg.trim()].length > MAX_SIGNUP_MESSAGE)
		return `Keep the message to ${MAX_SIGNUP_MESSAGE} characters or fewer.`;
	return '';
}
