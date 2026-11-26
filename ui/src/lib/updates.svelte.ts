import { api } from './api';
import { session } from './session.svelte';
import type { UpdatesStatus } from './updates';

/** The last GET /api/updates, shared by the nav badge, the Stack page and the Updates page. */
class UpdatesState {
	status = $state<UpdatesStatus | null>(null);
	error = $state('');
	#at = 0;

	get allowed() {
		return !!session.info?.features.updates && session.can('services.manage');
	}

	async load(force = false): Promise<UpdatesStatus | null> {
		if (!this.allowed) return null;
		if (!force && this.status && Date.now() - this.#at < 60_000) return this.status;
		try {
			this.status = await api<UpdatesStatus>('GET', '/updates');
			this.#at = Date.now();
			this.error = '';
		} catch (e) {
			this.error = e instanceof Error ? e.message : String(e);
		}
		return this.status;
	}

	set(st: UpdatesStatus) {
		this.status = st;
		this.#at = Date.now();
	}
}

export const updates = new UpdatesState();
