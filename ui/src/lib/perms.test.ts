import { describe, expect, it } from 'vitest';
import { firstAllowed, permFor, permLock, roleLock, vhostFor, vhostProblem, viewer } from './perms';
import type { Role } from './types';

const role = (id: string, position: number, builtin: Role['builtin'] = null): Role => ({
	id,
	name: id,
	color: null,
	position,
	builtin,
	permissions: [],
	irc: { whois_line: '', vhost: '', oper: false },
	hoist: false,
	mentionable: false,
	member_count: 0
});
const roles = [
	role('owner', 100, 'owner'),
	role('admin', 90, 'admin'),
	role('dj', 40),
	role('member', 0, 'member')
];

describe('page permissions', () => {
	it('maps paths to permissions', () => {
		expect(permFor('/')).toBe('services.view');
		expect(permFor('/users/usr_1')).toBe('users.view');
		expect(permFor('/roles')).toBe('roles.manage');
		expect(permFor('/usersx')).toBeUndefined();
		expect(permFor('/login')).toBeUndefined();
	});
	it('finds the first page someone can open', () => {
		expect(firstAllowed(['admin.panel', 'services.view', 'users.view'])).toBe('/');
		expect(firstAllowed(['admin.panel', 'users.view', 'users.moderate'])).toBe('/users');
		expect(firstAllowed(['admin.panel'])).toBeNull();
	});
});

describe('hierarchy', () => {
	const admin = viewer(
		[{ id: 'admin', name: 'Admin', color: null, position: 90 }],
		['roles.manage', 'users.view'],
		roles
	);
	const owner = viewer([{ id: 'owner', name: 'Owner', color: null, position: 100 }], [], roles);
	it('locks roles at or above the viewer', () => {
		expect(roleLock(roles[2], admin)).toBe('');
		expect(roleLock(roles[1], admin)).toMatch(/below your own/);
		expect(roleLock(roles[0], admin)).toMatch(/Owner/);
		expect(roleLock(roles[3], admin)).toBe(''); // Member is below everyone
	});
	it('lets Owner do anything', () => {
		expect(owner.owner).toBe(true);
		expect(roles.every((r) => roleLock(r, owner) === '')).toBe(true);
		expect(permLock('roles.owner', owner)).toBe('');
	});
	it('only grants held permissions', () => {
		expect(permLock('users.view', admin)).toBe('');
		expect(permLock('audit.view', admin)).toMatch(/don’t have/);
	});
	it('needs roles.manage', () => {
		const v = viewer([{ id: 'admin', name: 'Admin', color: null, position: 90 }], [], roles);
		expect(roleLock(roles[2], v)).toMatch(/Manage roles/);
	});
});

describe('vhosts', () => {
	it('expands {username}', () => {
		expect(vhostFor('{username}.staff.boson.chat', 'Ana')).toBe('ana.staff.boson.chat');
		expect(vhostFor('{USERNAME}.x.y', 'b')).toBe('b.x.y');
	});
	it('explains bad templates', () => {
		expect(vhostProblem('')).toBe('');
		expect(vhostProblem('{username}.staff.boson.chat')).toBe('');
		expect(vhostProblem('{nick}.staff.boson.chat')).toMatch(/only placeholder/);
		expect(vhostProblem('staff')).toMatch(/dot/);
		expect(vhostProblem('a..b')).toMatch(/empty part/);
		expect(vhostProblem('a b.c')).toMatch(/letters/);
	});
});
