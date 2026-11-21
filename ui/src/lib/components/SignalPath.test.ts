import { describe, expect, it } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import SignalPath from './SignalPath.svelte';
import type { ServiceHealth } from '$lib/types';

const ok = (url: string, ms: number) => ({ url, ok: true, latency_ms: ms });

const services: ServiceHealth[] = [
	{ name: 'auth', status: 'up', version: '1.19.0', checks: [ok('http://auth:8081/healthz', 2)] },
	{ name: 'web', status: 'up', version: '0.88.0', checks: [ok('http://web:8080/healthz', 1)] },
	{
		name: 'webircgateway',
		status: 'down',
		version: '1.1.0-boson.1',
		checks: [
			{ url: 'http://webircgateway:8080/webirc/info', ok: false, latency_ms: 0, detail: 'connection refused' }
		]
	}
];

function row(name: string) {
	return screen.getByRole('heading', { name }).closest('li') as HTMLElement;
}

describe('SignalPath', () => {
	it('shows the web client and its IRC gateway like the other services', () => {
		render(SignalPath, {
			services,
			certificates: [],
			discovered: true,
			probes: [
				{ name: 'web', url: 'https://chat.example.com/healthz', kind: 'http' },
				{ name: 'webircgateway', url: 'https://chat.example.com/webirc/info', kind: 'http' }
			],
			browser: {
				web: { ms: 24, samples: 4, at: 1 },
				webircgateway: { ms: null, samples: 0, error: 'unreachable', at: 1 }
			}
		});
		const web = within(row('web'));
		expect(web.getByText('Boson in the browser')).toBeTruthy();
		expect(web.getByText('0.88.0')).toBeTruthy();
		expect(web.getByText('Up')).toBeTruthy();
		expect(web.getByText('24 ms')).toBeTruthy();
		expect(web.getByText('1 ms')).toBeTruthy();

		const gw = within(row('webircgateway'));
		expect(gw.getByText('IRC gateway')).toBeTruthy();
		expect(gw.getByText('1.1.0-boson.1')).toBeTruthy();
		expect(gw.getByText('Down')).toBeTruthy();
		expect(gw.getByText('n/a')).toBeTruthy();
		expect(gw.getByText('http://webircgateway:8080/webirc/info')).toBeTruthy();

		// Services everyone knows by name get no role line.
		expect(row('auth').querySelector('.role')).toBeNull();
	});
});
