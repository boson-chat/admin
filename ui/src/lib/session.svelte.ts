import type { SessionInfo } from './types';

class SessionState {
	info = $state<SessionInfo | null>(null);
	loaded = $state(false);
	/** Why the admin was signed out, shown on the login page. */
	notice = $state('');

	set(info: SessionInfo) {
		this.info = info;
		this.loaded = true;
		this.notice = '';
	}

	/** Whether the signed-in account has every one of perms (the BFF enforces it too). */
	can(...perms: string[]) {
		const have = this.info?.permissions ?? [];
		return perms.every((p) => have.includes(p));
	}

	clear(notice = '') {
		this.info = null;
		this.loaded = true;
		this.notice = notice;
	}
}

export const session = new SessionState();
