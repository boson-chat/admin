import { api, isUnsupported } from './api';
import { errorText } from './toasts.svelte';
import { session } from './session.svelte';
import { viewer, type Viewer } from './perms';
import type { Permission, PermissionList, Role, SessionInfo } from './types';

/** The roles section's shared data: the role list and the permission catalog. */
class RolesState {
	roles = $state<Role[]>([]);
	permissions = $state<Permission[]>([]);
	loaded = $state(false);
	error = $state('');
	unsupported = $state('');

	/** The signed-in account in the role hierarchy. */
	viewer: Viewer = $derived(viewer(session.info?.roles ?? [], session.info?.permissions ?? [], this.roles));

	async load() {
		try {
			const [r, p] = await Promise.all([
				api<{ roles: Role[] }>('GET', '/roles'),
				api<PermissionList>('GET', '/permissions')
			]);
			this.roles = sorted(r.roles);
			this.permissions = p.permissions;
			this.error = this.unsupported = '';
		} catch (e) {
			if (isUnsupported(e)) this.unsupported = e.message;
			else this.error = errorText(e);
		} finally {
			this.loaded = true;
		}
	}

	byId(id: string) {
		return this.roles.find((r) => r.id === id);
	}

	/** Puts a saved role back into the list. */
	put(role: Role) {
		const i = this.roles.findIndex((r) => r.id === role.id);
		if (i >= 0) this.roles[i] = role;
		else this.roles.push(role);
		this.roles = sorted(this.roles);
	}

	remove(id: string) {
		this.roles = this.roles.filter((r) => r.id !== id);
	}
}

export function sorted(list: Role[]): Role[] {
	return [...list].sort((a, b) => b.position - a.position);
}

export const rolesState = new RolesState();

/** Whether a role keeps its place: Owner on top, Member at the bottom. */
export function pinned(r: Role) {
	return r.builtin === 'owner' || r.builtin === 'member';
}

/**
 * Moves the role at `from` to `to` in `list`, keeping pinned roles in place.
 * Returns the new order (unchanged when the move isn't allowed).
 */
export function moveRole(list: Role[], from: number, to: number, movable: (r: Role) => boolean): Role[] {
	if (from === to || from < 0 || from >= list.length) return list;
	if (!movable(list[from])) return list;
	const lo = list.findIndex((r) => movable(r));
	let hi = -1;
	list.forEach((r, i) => {
		if (movable(r)) hi = i;
	});
	const target = Math.min(Math.max(to, lo), hi);
	// every role between from and target must be movable too (no jumping over locked ones)
	const [a, b] = from < target ? [from, target] : [target, from];
	for (let i = a; i <= b; i++) if (!movable(list[i])) return list;
	const out = [...list];
	const [r] = out.splice(from, 1);
	out.splice(target, 0, r);
	return out;
}

/** Re-reads the session: a role change may have changed the viewer's own permissions. */
export async function refreshSession() {
	try {
		session.set(await api<SessionInfo>('GET', '/session'));
	} catch {
		/* the api helper handles a session that ended */
	}
}
