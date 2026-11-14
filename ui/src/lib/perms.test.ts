import { describe, expect, it } from 'vitest';
import {
	defaultWhois,
	firstAllowed,
	nameProblem,
	permFor,
	permLock,
	roleLock,
	userLock,
	vhostFor,
	vhostProblem,
	viewer
} from './perms';
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
	it('renders {username} like auth', () => {
		expect(vhostFor('{username}.staff.boson.chat', 'Ana')).toBe('ana.staff.boson.chat');
		expect(vhostFor('{username}.x', 'Bob[x]')).toBe('bob-x-.x');
		expect(vhostFor('staff/{username}', 'a\\b^c|d`e{f}')).toBe('staff/a-b-c-d-e-f-');
	});
	it('explains bad templates', () => {
		expect(vhostProblem('')).toBe('');
		expect(vhostProblem('{username}.staff.boson.chat')).toBe('');
		expect(vhostProblem('staff/{username}')).toBe('');
		expect(vhostProblem('users.boson.chat')).toBe('');
		expect(vhostProblem('{nick}.staff.boson.chat')).toMatch(/only placeholder/);
		expect(vhostProblem('a b.c')).toMatch(/letters/);
		expect(vhostProblem('.{username}.x')).toMatch(/start or end/);
		expect(vhostProblem('{username}.x/')).toMatch(/start or end/);
		expect(vhostProblem('a..b')).toMatch(/\.\./);
		expect(vhostProblem('a//b')).toMatch(/\/\//);
		expect(vhostProblem('{username}.' + 'a'.repeat(33))).toBe('');
		expect(vhostProblem('{username}.' + 'a'.repeat(34))).toMatch(/long/);
	});
});

describe('names and defaults', () => {
	it('checks role names like auth', () => {
		expect(nameProblem('Night shift')).toBe('');
		expect(nameProblem('ops_team-2')).toBe('');
		expect(nameProblem('DJ!')).toMatch(/letters/);
		expect(nameProblem(' ')).toMatch(/needs a name/);
		expect(nameProblem('x'.repeat(33))).toMatch(/32/);
	});
	it('words the default WHOIS line', () => {
		expect(defaultWhois('Moderator')).toBe('is a Moderator');
		expect(defaultWhois('Admin')).toBe('is an Admin');
	});
	it('locks users at or above the viewer', () => {
		const v = viewer([{ id: 'admin', name: 'Admin', color: null, position: 90 }], ['users.manage'], roles);
		const ref = (p: number) => [{ id: 'x', name: 'x', color: null, position: p }];
		expect(userLock(ref(40), v)).toBe('');
		expect(userLock([], v)).toBe('');
		expect(userLock(ref(90), v)).toMatch(/at or above/);
		expect(userLock(ref(90), v, true)).toBe('');
	});
});
