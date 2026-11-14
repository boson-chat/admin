import { describe, expect, it } from 'vitest';
import { moveRole, pinned } from './roles.svelte';
import type { Role } from './types';

const r = (id: string, builtin: Role['builtin'] = null) =>
	({ id, name: id, builtin, position: 0 }) as unknown as Role;
const list = [r('owner', 'owner'), r('admin', 'admin'), r('a'), r('b'), r('c'), r('member', 'member')];
const ids = (l: Role[]) => l.map((x) => x.id).join(',');
// an Admin viewer: Admin and above are locked
const movable = (x: Role) => !pinned(x) && x.id !== 'admin';

describe('moveRole', () => {
	it('moves within the movable range', () => {
		expect(ids(moveRole(list, 2, 4, movable))).toBe('owner,admin,b,c,a,member');
		expect(ids(moveRole(list, 4, 2, movable))).toBe('owner,admin,c,a,b,member');
	});
	it('clamps instead of passing pinned or locked roles', () => {
		expect(ids(moveRole(list, 2, 0, movable))).toBe(ids(list)); // a is already the top movable one
		expect(ids(moveRole(list, 3, 0, movable))).toBe('owner,admin,b,a,c,member');
		expect(ids(moveRole(list, 3, 9, movable))).toBe('owner,admin,a,c,b,member');
	});
	it('never moves pinned or locked roles', () => {
		expect(moveRole(list, 0, 3, movable)).toBe(list);
		expect(moveRole(list, 1, 3, movable)).toBe(list);
		expect(moveRole(list, 5, 2, movable)).toBe(list);
	});
});
