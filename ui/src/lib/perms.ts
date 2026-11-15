import type { Role, RoleRef } from './types';

/** What each page needs (on top of admin.panel). The BFF enforces the same table. */
export const pagePerms: { prefix: string; perm: string; label: string }[] = [
	{ prefix: '/services', perm: 'services.view', label: 'Services' },
	{ prefix: '/settings', perm: 'settings.manage', label: 'Network settings' },
	{ prefix: '/branding', perm: 'settings.manage', label: 'Branding' },
	{ prefix: '/emails', perm: 'settings.manage', label: 'Emails' },
	{ prefix: '/nick-registration', perm: 'settings.manage', label: 'Nick registration' },
	{ prefix: '/uploads', perm: 'settings.manage', label: 'Uploads' },
	{ prefix: '/directory', perm: 'directory.manage', label: 'Directory' },
	{ prefix: '/audit', perm: 'audit.view', label: 'Audit log' },
	{ prefix: '/users', perm: 'users.view', label: 'Users' },
	{ prefix: '/files', perm: 'users.moderate', label: 'Uploaded files' },
	{ prefix: '/roles', perm: 'roles.manage', label: 'Roles' },
	{ prefix: '/invites', perm: 'invites.manage', label: 'Invites' },
	{ prefix: '/sso', perm: 'sso.manage', label: 'Sign-in providers' }
];
/** The stack overview at "/" shows service health. */
export const homePerm = 'services.view';

/** The permission a path needs, or undefined when admin.panel is enough. */
export function permFor(path: string): string | undefined {
	if (path === '/') return homePerm;
	return pagePerms.find((p) => path === p.prefix || path.startsWith(p.prefix + '/'))?.perm;
}

/** The first page someone with these permissions can open, or null. */
export function firstAllowed(perms: Iterable<string>): string | null {
	const has = new Set(perms);
	if (has.has(homePerm)) return '/';
	return pagePerms.find((p) => has.has(p.perm))?.prefix ?? null;
}

/** The viewer as the role hierarchy sees them. */
export interface Viewer {
	/** Position of their most senior role (-1: none but Member). */
	top: number;
	owner: boolean;
	perms: Set<string>;
}

/**
 * Builds the viewer. `roles` (with built-ins) tells whether one of the
 * viewer's roles is Owner, which bypasses the hierarchy.
 */
export function viewer(mine: RoleRef[], perms: string[], roles: Role[] = []): Viewer {
	const ids = new Set(mine.map((r) => r.id));
	return {
		top: mine.reduce((t, r) => Math.max(t, r.position), -1),
		owner: roles.some((r) => r.builtin === 'owner' && ids.has(r.id)),
		perms: new Set(perms)
	};
}

/** Why the viewer can't edit (or assign) this role, or '' when they can. */
export function roleLock(role: Role, v: Viewer): string {
	if (v.owner) return '';
	if (!v.perms.has('roles.manage')) return 'You need the “Manage roles” permission.';
	if (role.builtin === 'owner')
		return v.perms.has('roles.owner')
			? ''
			: 'Only someone with “Manage the Owner role” can change the Owner role.';
	if (role.builtin !== 'member' && role.position >= v.top)
		return 'You can only change roles below your own highest role.';
	return '';
}

/** Why the viewer can't toggle this permission on a role they can edit, or ''. */
export function permLock(perm: string, v: Viewer): string {
	if (v.owner || v.perms.has(perm)) return '';
	return 'You can’t grant a permission you don’t have yourself.';
}

/** Whether a colour is a #rrggbb hex. */
export function hexOk(c: string | null | undefined): c is string {
	return !!c && /^#[0-9a-f]{6}$/i.test(c);
}

/**
 * Why the viewer can't act on this user (disable, revoke sessions, change
 * roles), or '': auth needs the user's highest role below the viewer's.
 */
export function userLock(target: RoleRef[] | undefined, v: Viewer, self = false): string {
	if (v.owner || self || !target) return '';
	const top = target.reduce((t, r) => Math.max(t, r.position), -1);
	return top >= v.top ? 'Their highest role is at or above yours.' : '';
}

/** auth's default WHOIS line for a role: "is a Moderator", "is an Admin". */
export function defaultWhois(name: string): string {
	return (/^[aeiou]/i.test(name.trim()) ? 'is an ' : 'is a ') + name.trim();
}

/**
 * Renders a vhost template for an account the way auth does: {username} is
 * the name in ASCII lower case with the nick characters Ergo rejects in
 * vhosts ([ ] \ ` ^ { | }) turned into "-".
 */
export function vhostFor(template: string, username: string): string {
	const u = username.replace(/[A-Z]/g, (c) => c.toLowerCase()).replace(/[[\]\\`^{|}]/g, '-');
	return template.replaceAll('{username}', u);
}

/** A problem with a vhost template (auth's rules), or '' when it is valid. */
export function vhostProblem(template: string): string {
	const t = template.trim();
	if (!t) return '';
	const stat = t.replaceAll('{username}', 'u');
	if (!/^[0-9A-Za-z.\-_/]+$/.test(stat))
		return 'Use letters, digits, . - _ / and {username} only (the only placeholder is {username}).';
	if (/^[./]|[./]$/.test(stat)) return 'A vhost can’t start or end with . or /.';
	if (stat.includes('..') || stat.includes('//')) return 'A vhost can’t contain .. or //.';
	if (new TextEncoder().encode(t.replaceAll('{username}', 'u'.repeat(30))).length > 64)
		return 'Too long: at most 64 characters with a 30-character username.';
	return '';
}

/** A problem with a role name (auth's rules), or ''. */
export function nameProblem(name: string): string {
	const n = name.trim().replace(/\s+/g, ' ');
	if (!n) return 'A role needs a name.';
	if (n.length > 32) return 'At most 32 characters.';
	if (!/^[A-Za-z0-9 _-]+$/.test(n)) return 'Use letters, digits, spaces, - and _ only.';
	return '';
}
