import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import StackChanges from './StackChanges.svelte';
import type { StackStatus } from '$lib/updates';

const im = {
	restart: true,
	disruptive: true,
	chat_disconnect: true,
	drops_calls: false,
	downtime_seconds: 10,
	who: 'x'
};

function status(over: Partial<StackStatus> = {}): StackStatus {
	return {
		version: '1.4.0',
		baseline: true,
		files: [
			{
				path: 'compose.yaml',
				action: 'update',
				diff: ['--- a', '+++ b', '@@ -1 +1 @@', '-    image: traefik:v3.7.19', '+    image: traefik:v3.7.20']
			},
			{ path: 'traefik/dynamic/stack.yml', action: 'local' },
			{ path: 'monitoring/scrape.yml', action: 'update', services: ['victoriametrics'] }
		],
		unchanged: 80,
		migrations: [
			{ name: '0001-bridge-client-webirc.sh', about: 'Bridge: the WEBIRC password', state: 'pending' },
			{ name: '0000-old.sh', state: 'done' }
		],
		images: [
			{ service: 'traefik', name: 'traefik', from: 'traefik:v3.7.19', to: 'traefik:v3.7.20', changed: true }
		],
		pins: [
			{
				env_var: 'AUTH_IMAGE',
				pin: 'harbor.floret.dev/boson/auth:1.20.0',
				default: 'harbor.floret.dev/boson/auth:1.27.0',
				action: 'raise',
				to: 'harbor.floret.dev/boson/auth:1.27.0',
				note: 'older than the release’s default: raised to it'
			},
			{
				env_var: 'QUICVOICE_IMAGE',
				pin: 'harbor.floret.dev/boson/quicvoice:1.16.0',
				default: 'harbor.floret.dev/boson/quicvoice:1.15.1',
				action: 'keep',
				to: 'harbor.floret.dev/boson/quicvoice:1.16.0',
				note: 'newer than the release’s default: kept'
			}
		],
		included: [
			{
				service: 'ergo',
				name: 'Ergo',
				from: 'harbor.floret.dev/boson/stack-ergo:2.19.1',
				to: 'harbor.floret.dev/boson/stack-ergo:1.4.0',
				changed: true
			},
			{
				service: 'certsync',
				name: 'certsync',
				from: 'harbor.floret.dev/boson/stack-certsync:1.4.0',
				to: 'harbor.floret.dev/boson/stack-certsync:1.4.0',
				changed: false
			}
		],
		restarts: ['ergo', 'roleserv', 'traefik', 'victoriametrics'],
		kept: ['evrd-links'],
		impacts: [],
		impact: im,
		analyzed_at: '2026-11-27T10:00:00Z',
		...over
	};
}

describe('StackChanges', () => {
	it('shows what the release changes here', () => {
		render(StackChanges, { s: status() });
		expect(screen.getByText('ergo, roleserv, traefik, victoriametrics')).toBeTruthy();
		expect(screen.getByText('evrd-links')).toBeTruthy();
		expect(screen.getByText('stack-ergo:2.19.1 → stack-ergo:1.4.0')).toBeTruthy();
		expect(screen.getByText('stack-certsync:1.4.0, unchanged')).toBeTruthy();
		expect(screen.getByText('auth:1.20.0 → auth:1.27.0')).toBeTruthy();
		expect(screen.getByText(/newer than the release’s default: kept/)).toBeTruthy();
		expect(screen.getByText('0001-bridge-client-webirc.sh')).toBeTruthy();
		expect(screen.queryByText('0000-old.sh')).toBeNull();
		expect(screen.getByText('Files: 3 changed, 80 unchanged')).toBeTruthy();
		expect(
			screen.getByText(/changed on this server since they were installed|A file was changed/)
		).toBeTruthy();
		expect(screen.getByText('restarts victoriametrics')).toBeTruthy();
		const added = document.querySelector('.diff .dadd');
		expect(added?.textContent).toContain('image: traefik:v3.7.20');
		expect(document.querySelector('.diff .ddel')?.textContent).toContain('traefik:v3.7.19');
	});
	it('says when the install is from before releases, and when it can’t be applied', () => {
		render(StackChanges, {
			s: status({ pre_release: true, baseline: false, error: 'this release needs admin panel 1.30.0' })
		});
		expect(screen.getByText(/from before stack releases/)).toBeTruthy();
		expect(screen.getByText(/needs admin panel 1.30.0/)).toBeTruthy();
	});
});
