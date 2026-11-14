import type { Role, RoleRef } from './types';

/** What each page needs (on top of admin.panel). The BFF enforces the same table. */
export const pagePerms: { prefix: string; perm: string; label: string }[] = [
	{ prefix: '/services', perm: 'services.view', label: 'Services' },
	{ prefix: '/settings', perm: 'settings.manage', label: 'Network settings' },
	{ prefix: '/directory', perm: 'directory.manage', label: 'Directory' },
	{ prefix: '/audit', perm: 'audit.view', label: 'Audit log' },
	{ prefix: '/users', perm: 'users.view', label: 'Users' },
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
	if (role.builtin === 'owner' && !v.perms.has('roles.owner'))
		return 'Only someone with “Manage the Owner role” can change the Owner role.';
	if (role.builtin !== 'member' && role.position >= v.top)
		return 'You can only change roles below your own highest role.';
	return '';
}

/** Why the viewer can't toggle this permission on a role they can edit, or ''. */
export function permLock(perm: string, v: Viewer): string {
	if (v.owner || v.perms.has(perm)) return '';
	return 'You can’t grant a permission you don’t have yourself.';
}

/** Readable text on a role colour chip: the colour itself, or the default ink. */
export function hexOk(c: string | null | undefined): c is string {
	return !!c && /^#[0-9a-f]{6}$/i.test(c);
}

/** Expands a vhost template for a nick, the way the IRC server will. */
export function vhostFor(template: string, username: string): string {
	return template.replaceAll(/\{username\}/gi, username.toLowerCase());
}

/** A problem with a vhost template, or '' when it looks valid. */
export function vhostProblem(template: string): string {
	const t = template.trim();
	if (!t) return '';
	if (/\{(?!username\})[^}]*\}/i.test(t)) return 'The only placeholder is {username}.';
	const plain = t.replaceAll(/\{username\}/gi, 'x');
	if (!/^[a-z0-9.-]+$/i.test(plain)) return 'Use letters, digits, dashes, dots and {username} only.';
	if (!plain.includes('.')) return 'A vhost needs at least one dot, like {username}.staff.example.com.';
	if (plain.split('.').some((l) => !l)) return 'There is an empty part between two dots.';
	if (t.replaceAll(/\{username\}/gi, '').length > 63)
		return 'Keep it under 64 characters besides {username}.';
	return '';
}
