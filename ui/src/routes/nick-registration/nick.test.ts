import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import type { ConfigView } from '$lib/types';
import type { NickClaimView } from '$lib/nickclaim';

const api = vi.hoisted(() => vi.fn());
vi.mock('$lib/api', () => {
	class ApiError extends Error {
		constructor(
			public status: number,
			public code: string,
			message: string,
			public field?: string
		) {
			super(message);
		}
	}
	return { api, ApiError, isUnsupported: () => false };
});

import Page from './+page.svelte';

const view: ConfigView = {
	enabled: true,
	exists: true,
	etag: '"v1"',
	issues: [],
	keys: [
		'nick_claim.enabled',
		'nick_claim.email_domain',
		'nick_claim.rate_limit_per_hour',
		'nick_claim.inbound.protocol',
		'nick_claim.inbound.host',
		'nick_claim.inbound.port',
		'nick_claim.inbound.tls',
		'nick_claim.inbound.user'
	].map((key) => ({
		key,
		kind: 'string',
		perm: 'settings.manage',
		services: ['api'],
		set: false,
		editable: true
	}))
};

const nick = (over: Partial<NickClaimView> = {}): NickClaimView => ({
	configured: true,
	password_env: 'NICK_CLAIM_POP3_PASSWORD',
	checked_at: '2026-11-14T10:00:00Z',
	status: {
		enabled: false,
		email_domain: '',
		inbound: { host: '', port: 995, tls: true, user: '', password_set: false },
		worker: {
			running: false,
			last_drain_at: null,
			last_ok_at: null,
			last_error: '',
			consecutive_failures: 0
		},
		pending_claims: 2
	},
	...over
});

function serve(n: NickClaimView) {
	api.mockImplementation(async (method: string, path: string) => {
		if (path === '/config' && method === 'GET') return view;
		if (path === '/config' && method === 'PATCH') return view;
		if (path === '/config/status') return { services: [] };
		if (path === '/nick-claims') return n;
		if (path === '/nick-claims/test')
			return { ok: true, message: 'Connected; 3 message(s) waiting.', messages: 3 };
		throw new Error('unexpected ' + method + ' ' + path);
	});
}

const save = () => screen.getByRole('button', { name: 'Save changes' });

describe('nick registration page', () => {
	beforeEach(() => {
		api.mockReset();
	});

	it('blocks enabling without a domain or mail server, inline', async () => {
		serve(nick());
		const user = userEvent.setup();
		render(Page);
		await user.click(await screen.findByRole('switch', { name: /Register nicks on other networks/ }));
		expect(screen.getAllByText('Needed to turn nick registration on.')).toHaveLength(2);
		expect(screen.getByText(/Fix the highlighted fields/)).toBeTruthy();
		expect((save() as HTMLButtonElement).disabled).toBe(true);

		await user.type(screen.getByRole('textbox', { name: 'Email domain' }), 'nick.example.com');
		await user.type(screen.getByRole('textbox', { name: 'Mail server' }), 'pop.example.com');
		expect(screen.queryByText('Needed to turn nick registration on.')).toBeNull();
		await user.click(save());
		await waitFor(() =>
			expect(api).toHaveBeenCalledWith(
				'PATCH',
				'/config',
				{
					set: {
						'nick_claim.enabled': true,
						'nick_claim.email_domain': 'nick.example.com',
						'nick_claim.inbound.host': 'pop.example.com',
						'nick_claim.inbound.protocol': 'pop3'
					}
				},
				{ 'If-Match': '"v1"' }
			)
		);
	});

	it('shows the password status and worker card', async () => {
		serve(nick());
		render(Page);
		expect((await screen.findByTestId('password-state')).textContent).toContain('Missing');
		expect(screen.getAllByText('NICK_CLAIM_POP3_PASSWORD').length).toBeGreaterThan(0);
		expect(screen.getByTestId('worker-state').textContent).toContain('Off');
		expect(screen.getByTestId('dedicated-warning').textContent).toMatch(/Use a mailbox dedicated to this/);
		expect(screen.getByText('Pending claims').nextElementSibling?.textContent?.trim()).toBe('2');
	});

	it('tests unsaved values, asking for a password for another server', async () => {
		const n = nick();
		n.status!.inbound = {
			host: 'pop.old.com',
			port: 995,
			tls: true,
			user: 'reg@old.com',
			password_set: true
		};
		serve(n);
		const user = userEvent.setup();
		render(Page);
		const host = await screen.findByRole('textbox', { name: 'Mail server' });
		expect((host as HTMLInputElement).value).toBe('pop.old.com');
		expect(screen.queryByLabelText('Password for this test')).toBeNull();
		expect(screen.getByText(/never\s+reads or deletes anything/)).toBeTruthy();
		await user.clear(host);
		await user.type(host, 'pop.new.com');
		await user.type(screen.getByLabelText('Password for this test'), 's3cret');
		await user.click(screen.getByRole('button', { name: 'Test connection' }));
		expect(await screen.findByText(/3 message\(s\) waiting/)).toBeTruthy();
		expect(api).toHaveBeenCalledWith('POST', '/nick-claims/test', {
			inbound: {
				protocol: 'pop3',
				host: 'pop.new.com',
				port: 995,
				tls: true,
				user: 'reg@old.com',
				password: 's3cret'
			}
		});
		// Nothing was saved.
		expect(api.mock.calls.some(([m]) => m === 'PATCH')).toBe(false);
	});

	it('says when the api isn’t reachable from the panel', async () => {
		serve({
			configured: false,
			password_env: 'NICK_CLAIM_POP3_PASSWORD',
			checked_at: '2026-11-14T10:00:00Z'
		});
		render(Page);
		expect(await screen.findByText(/The api service isn’t reachable from the admin panel/)).toBeTruthy();
		expect((screen.getByRole('button', { name: 'Test connection' }) as HTMLButtonElement).disabled).toBe(
			true
		);
	});
});
