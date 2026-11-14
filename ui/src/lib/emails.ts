/** The emails auth sends whose wording the Emails page edits (auth.email.templates.*). */
export const EMAIL_TEMPLATES = [
	{
		id: 'verification',
		label: 'Email verification',
		detail: 'Sent when someone registers, to confirm their address.',
		vars: ['network', 'username', 'link', 'expires']
	},
	{
		id: 'password_reset',
		label: 'Password reset',
		detail: 'Sent when someone asks to reset their password, or an admin sends a reset link.',
		vars: ['network', 'username', 'link', 'expires']
	},
	{
		id: 'account_setup',
		label: 'Account setup',
		detail: 'Sent when an admin creates an account, so the person can choose a password.',
		vars: ['network', 'username', 'link', 'expires']
	},
	{
		id: 'invite',
		label: 'Invite',
		detail: 'Sent with an invite code when registration is invite only.',
		vars: ['network', 'inviter', 'link', 'expires']
	}
] as const;

export type EmailTemplateId = (typeof EMAIL_TEMPLATES)[number]['id'];

export const VAR_HELP: Record<string, string> = {
	network: 'The network’s name',
	username: 'The account’s username',
	link: 'The link to click',
	expires: 'How long the link works, like “24 hours”',
	inviter: 'Who sent the invite'
};

export const MAX_SUBJECT = 200;
export const MAX_BODY = 5000;

export const subjectKey = (t: string) => `auth.email.templates.${t}.subject`;
export const bodyKey = (t: string) => `auth.email.templates.${t}.body`;

/** {{names}} the text uses that this template doesn't fill (left as literal text by auth). */
export function unknownVars(text: string, allowed: readonly string[]): string[] {
	const out = new Set<string>();
	for (const m of text.matchAll(/\{\{\s*([a-zA-Z_]+)\s*\}\}/g)) if (!allowed.includes(m[1])) out.add(m[1]);
	return [...out];
}

/** Inserts text at the textarea/input's caret (or replaces the selection); returns the new value and caret. */
export function insertAt(
	value: string,
	start: number,
	end: number,
	text: string
): { value: string; caret: number } {
	const s = Math.max(0, Math.min(start, value.length));
	const e = Math.max(s, Math.min(end, value.length));
	return { value: value.slice(0, s) + text + value.slice(e), caret: s + text.length };
}

/** Rough check of one email address (auth checks again). */
export function emailOk(s: string): boolean {
	return /^[^\s@<>,;]+@[^\s@<>,;]+\.[^\s@<>,;]+$/.test(s.trim());
}
