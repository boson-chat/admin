<!--
	Which services have picked up the config file, and a Restart for the
	ones that need one for a key (through the Docker socket proxy, with the
	same confirmation as the Services page).
-->
<script lang="ts">
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { serviceNames } from '$lib/stackconfig.svelte';
	import type { ConfigServiceStatus, ServiceInfo, ServiceList } from '$lib/types';
	import Confirm from './Confirm.svelte';
	import Time from './Time.svelte';

	let {
		statuses,
		checking = false,
		onrecheck
	}: { statuses: ConfigServiceStatus[]; checking?: boolean; onrecheck?: () => void } = $props();

	const word: Record<string, string> = {
		applied: 'Applied',
		pending: 'Loading…',
		restart_required: 'Needs a restart',
		error: 'Rejected the file',
		unreachable: 'Not answering',
		unsupported: 'Too old to report'
	};
	const tone: Record<string, string> = {
		applied: 'tag-ok',
		pending: '',
		restart_required: 'tag-warn',
		error: 'tag-bad',
		unreachable: 'tag-warn',
		unsupported: ''
	};

	let canRestart = $derived(!!session.info?.features.services && session.can('services.manage'));
	let containers = $state<ServiceInfo[] | null>(null);
	async function containerFor(service: string): Promise<ServiceInfo | undefined> {
		if (!containers) containers = (await api<ServiceList>('GET', '/services')).services ?? [];
		return containers.find((c) => c.service === service);
	}

	async function restart(name: string) {
		try {
			const c = await containerFor(name);
			if (!c) throw new Error(`No ${name} container in the stack.`);
			const r = await api<{ deferred?: boolean }>('POST', `/services/${encodeURIComponent(c.name)}/restart`, {
				confirm: c.service
			});
			toasts.push(
				r.deferred
					? 'Restarting the admin panel. Reload this page in a few seconds.'
					: `Restarted ${c.service}.`
			);
			setTimeout(() => onrecheck?.(), 3000);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			throw e;
		}
	}
	const protectedSvc = (n: string) => n === 'admin' || n === 'traefik';
</script>

<div class="wrap">
	<ul class="svcs" aria-busy={checking}>
		{#each statuses as s (s.name)}
			<li data-state={s.state}>
				<span class="dot" aria-hidden="true"></span>
				<div class="who">
					<span class="name">{serviceNames[s.name] ?? s.name}</span>
					<span class="reads">reads {s.sections.join(', ') || 'nothing'}</span>
				</div>
				<div class="what">
					<span class="tag {tone[s.state]}">{word[s.state] ?? s.state}</span>
					{#if s.state === 'restart_required'}
						<span class="small">for <code>{s.restart_required.join(', ')}</code></span>
					{:else if s.state === 'error' && s.error}
						<span class="small err">{s.error}. It keeps its last good settings.</span>
					{:else if s.state === 'unreachable'}
						<span class="small muted">{s.error}</span>
					{:else if s.state === 'unsupported'}
						<span class="small muted"
							>This release doesn’t report on the file; restart it after a change to be sure.</span
						>
					{:else if s.loaded_at}
						<span class="small muted">loaded <Time at={s.loaded_at} /></span>
					{/if}
				</div>
				<div class="act">
					{#if canRestart && (s.state === 'restart_required' || s.state === 'unsupported')}
						<Confirm
							title="Restart {s.name}?"
							action="Restart"
							danger={protectedSvc(s.name)}
							typed={protectedSvc(s.name) ? s.name : undefined}
							onconfirm={() => restart(s.name)}
						>
							{#snippet trigger(open)}
								<button class="btn btn-small" onclick={open}>Restart</button>
							{/snippet}
							{s.name} stops (up to 10 seconds to exit cleanly) and starts again with the new settings. Open connections
							to it drop.
						</Confirm>
					{/if}
				</div>
			</li>
		{/each}
	</ul>
	{#if onrecheck}
		<button class="btn btn-small btn-ghost recheck" onclick={onrecheck} disabled={checking}
			>{checking ? 'Checking…' : 'Check again'}</button
		>
	{/if}
</div>

<style>
	.wrap {
		display: grid;
		gap: var(--space-2);
	}
	.svcs {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	li {
		display: grid;
		grid-template-columns: auto 11rem minmax(0, 1fr) auto;
		gap: var(--space-3);
		align-items: center;
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}
	li:last-child {
		border-bottom: 0;
	}
	.dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--fg-quiet);
	}
	[data-state='applied'] .dot {
		background: var(--success);
	}
	[data-state='restart_required'] .dot,
	[data-state='unreachable'] .dot {
		background: var(--warn);
	}
	[data-state='error'] .dot {
		background: var(--danger);
	}
	.who {
		display: grid;
	}
	.name {
		font-weight: 600;
		font-size: var(--text-sm);
	}
	.reads {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.what {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		min-width: 0;
	}
	.small {
		font-size: var(--text-xs);
	}
	.err {
		color: var(--danger-text);
	}
	.recheck {
		justify-self: end;
	}
	@media (max-width: 640px) {
		li {
			grid-template-columns: auto minmax(0, 1fr);
		}
		.what,
		.act {
			grid-column: 2;
		}
	}
</style>
