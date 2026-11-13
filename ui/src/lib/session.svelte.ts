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

	clear(notice = '') {
		this.info = null;
		this.loaded = true;
		this.notice = notice;
	}
}

export const session = new SessionState();
