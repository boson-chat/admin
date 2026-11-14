<script lang="ts">
	import { api } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import type { ServiceInfo } from '$lib/types';
	import Confirm from './Confirm.svelte';
	import { session } from '$lib/session.svelte';

	/** Start / stop / restart buttons for one container, with confirmation. */
	let {
		svc,
		ondone,
		compact = false
	}: { svc: ServiceInfo; ondone?: (s?: ServiceInfo) => void; compact?: boolean } = $props();

	let running = $derived(svc.state === 'running' || svc.state === 'restarting');

	async function act(action: 'start' | 'stop' | 'restart') {
		try {
			const r = await api<{ service: ServiceInfo; deferred?: boolean }>(
				'POST',
				`/services/${encodeURIComponent(svc.name)}/${action}`,
				{ confirm: svc.service }
			);
			const verb = { start: 'Started', stop: 'Stopped', restart: 'Restarted' }[action];
			toasts.push(
				r.deferred
					? action === 'stop'
						? 'Stopping the admin panel. Start it again on the server: docker compose up -d admin.'
						: 'Restarting the admin panel. Reload this page in a few seconds.'
					: `${verb} ${svc.service}.`
			);
			ondone?.(r.service);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			throw e;
		}
	}

	function consequence(action: 'stop' | 'restart'): string {
		if (svc.self)
			return action === 'stop'
				? 'This is the admin panel itself. Once stopped, it can only be started again from the server (docker compose up -d admin).'
				: 'This is the admin panel itself. It goes away for a few seconds and this page reconnects.';
		if (svc.service === 'traefik' || svc.protected)
			return action === 'stop'
				? `Everything public goes through ${svc.service}: the apps, sign-in and this panel stop being reachable, and you can’t start it again from here.`
				: `Everything public goes through ${svc.service}; every connection drops for a few seconds, including this page.`;
		return action === 'stop'
			? `${svc.service} stays stopped until someone starts it again. Anything that depends on it fails meanwhile.`
			: `${svc.service} is stopped (up to 10 seconds to exit cleanly) and started again. Open connections to it drop.`;
	}
</script>

<div class="acts" class:compact>
	{#if !session.can('services.manage')}
		<!-- restart / stop / start need services.manage -->
	{:else if running}
		<Confirm
			title="Restart {svc.service}?"
			action="Restart"
			danger={svc.protected}
			typed={svc.protected ? svc.service : undefined}
			onconfirm={() => act('restart')}
		>
			{#snippet trigger(open)}
				<button class="btn btn-small" onclick={open} aria-label="Restart {svc.name}">Restart</button>
			{/snippet}
			{consequence('restart')}
		</Confirm>
		<Confirm
			title="Stop {svc.service}?"
			action="Stop"
			danger
			typed={svc.protected ? svc.service : undefined}
			onconfirm={() => act('stop')}
		>
			{#snippet trigger(open)}
				<button class="btn btn-small btn-ghost stop" onclick={open} aria-label="Stop {svc.name}">Stop</button>
			{/snippet}
			{consequence('stop')}
		</Confirm>
	{:else}
		<Confirm title="Start {svc.service}?" action="Start" onconfirm={() => act('start')}>
			{#snippet trigger(open)}
				<button class="btn btn-small btn-primary" onclick={open} aria-label="Start {svc.name}">Start</button>
			{/snippet}
			Starts the existing {svc.name} container with its current image and settings.
		</Confirm>
	{/if}
</div>

<style>
	.acts {
		display: flex;
		gap: var(--space-2);
		flex-wrap: wrap;
	}
	.stop:hover:not(:disabled) {
		color: var(--danger-text);
	}
</style>
