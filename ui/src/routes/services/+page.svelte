<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { errorText } from '$lib/toasts.svelte';
	import { bytes, relative, since } from '$lib/format';
	import type { ServiceInfo, ServiceList } from '$lib/types';
	import ServiceActions from '$lib/components/ServiceActions.svelte';
	import State from '$lib/components/State.svelte';

	let data = $state<ServiceList | null>(null);
	let error = $state('');
	let loading = $state(false);
	let now = $state(Date.now());

	async function load() {
		loading = true;
		try {
			data = await api<ServiceList>('GET', '/services');
			error = '';
		} catch (e) {
			error = errorText(e);
		} finally {
			loading = false;
		}
	}
	onMount(() => {
		load();
		const poll = setInterval(() => {
			if (document.visibilityState === 'visible') load();
		}, 10000);
		const tick = setInterval(() => (now = Date.now()), 1000);
		return () => {
			clearInterval(poll);
			clearInterval(tick);
		};
	});

	function tone(s: ServiceInfo): 'ok' | 'warn' | 'bad' | 'off' {
		if (s.state === 'running')
			return s.health === 'unhealthy' ? 'bad' : s.health === 'starting' ? 'warn' : 'ok';
		if (s.state === 'restarting') return 'bad';
		if (s.state === 'exited' && s.exit_code === 0) return 'off';
		return 'bad';
	}
	function stateText(s: ServiceInfo): string {
		if (s.state === 'running') {
			const h = s.health === 'unhealthy' ? ', unhealthy' : s.health === 'starting' ? ', starting' : '';
			return `Up ${since(s.started_at, now)}${h}`;
		}
		if (s.state === 'restarting') return `Restarting (exit ${s.exit_code})`;
		if (s.state === 'exited')
			return `Stopped${s.exit_code ? ` (exit ${s.exit_code}${s.oom_killed ? ', out of memory' : ''})` : ''}${s.finished_at ? ' ' + relative(s.finished_at, now) : ''}`;
		return s.state.charAt(0).toUpperCase() + s.state.slice(1);
	}
	function memShare(s: ServiceInfo) {
		if (!s.memory_bytes || !s.memory_limit) return 0;
		return Math.min(1, s.memory_bytes / s.memory_limit);
	}

	let summary = $derived.by(() => {
		if (!data?.services.length) return '';
		const n = data.services.length;
		const bad = data.services.filter((s) => tone(s) === 'bad').map((s) => s.service);
		const off = data.services.filter((s) => tone(s) === 'off').length;
		if (bad.length) return `${bad.join(', ')} ${bad.length === 1 ? 'needs' : 'need'} attention.`;
		return `${n - off} of ${n} containers running${off ? `, ${off} stopped on purpose` : ''}.`;
	});
</script>

<svelte:head><title>Services · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Services</h1>
		<p>
			{#if summary}{summary}{:else}The containers of the stack: restart one that’s stuck, stop or start it,
				and read its logs.{/if}
		</p>
	</div>
	{#if data?.configured}
		<div class="head-acts">
			<span class="muted checked">Project <code>{data.project}</code></span>
			<button class="btn btn-small" onclick={load} disabled={loading}
				>{loading ? 'Checking…' : 'Refresh'}</button
			>
		</div>
	{/if}
</div>

{#if error}
	<State kind="error">
		{error}
		{#snippet action()}<button class="btn btn-small" onclick={load}>Try again</button>{/snippet}
	</State>
{:else if !data}
	<State kind="loading" title="Asking Docker…" />
{:else if !data.configured}
	<State kind="off" title="Services control is off">
		<p>
			The panel controls containers through a locked-down Docker socket proxy that only allows listing, logs,
			stats, start, stop and restart. Add the proxy to the stack and set <code>DOCKER_PROXY_URL</code>, for
			example <code>http://docker-proxy:2375</code>. The README has the compose snippet.
		</p>
		<p>The Docker socket itself is never mounted into the panel.</p>
	</State>
{:else if data.error}
	<State kind="error" title="Docker didn’t answer">
		{data.error}
		{#snippet action()}<button class="btn btn-small" onclick={load}>Try again</button>{/snippet}
	</State>
{:else if data.services.length === 0}
	<State kind="empty" title="No containers in “{data.project}”">
		The proxy answered, but no container carries the compose label <code
			>com.docker.compose.project={data.project}</code
		>. Set <code>DOCKER_PROJECT</code> to the stack’s project name.
	</State>
{:else}
	<ul class="rack" aria-busy={loading}>
		{#each data.services as s (s.name)}
			{@const t = tone(s)}
			<li data-tone={t}>
				<span class="lamp" aria-hidden="true"></span>
				<div class="id">
					<a class="svc" href="/services/{encodeURIComponent(s.name)}">{s.service}</a>
					{#if s.self}<span class="tag tag-accent">This panel</span>{:else if s.protected}<span class="tag"
							>Protected</span
						>{/if}
					<div class="sub mono" title={s.image}>{s.name} · {s.version || s.image}</div>
				</div>
				<div class="state">
					<span class="st">{stateText(s)}</span>
					{#if s.restart_count}<span class="restarts"
							>{s.restart_count} restart{s.restart_count === 1 ? '' : 's'}</span
						>{/if}
				</div>
				<div class="res">
					{#if s.cpu_percent !== undefined}
						<span class="cpu"><span class="k">CPU</span> {s.cpu_percent.toFixed(1)}%</span>
						<span class="mem" title={s.memory_limit ? `of ${bytes(s.memory_limit)}` : ''}>
							<span class="k">MEM</span>
							{bytes(s.memory_bytes)}
							{#if s.memory_limit}<span class="bar"><span style:width="{memShare(s) * 100}%"></span></span
								>{/if}
						</span>
					{:else}
						<span class="muted">—</span>
					{/if}
				</div>
				<div class="ops">
					<a class="btn btn-small btn-ghost" href="/services/{encodeURIComponent(s.name)}">Logs</a>
					<ServiceActions svc={s} ondone={() => setTimeout(load, 600)} />
				</div>
			</li>
		{/each}
	</ul>
	<p class="muted foot">
		Refreshes every 10 seconds. Restarts and stops are logged with your name. Stopping or restarting
		{data.services
			.filter((s) => s.protected)
			.map((s) => s.service)
			.join(' or ') || 'the panel or the edge'} asks you to type its name.
	</p>
{/if}

<style>
	.head-acts {
		display: flex;
		align-items: center;
		gap: var(--space-3);
	}
	.checked {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}
	.rack {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.rack li {
		display: grid;
		grid-template-columns: 12px minmax(160px, 1.3fr) minmax(140px, 1fr) minmax(170px, 1fr) auto;
		gap: var(--space-4);
		align-items: center;
		padding: var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}
	.rack li:last-child {
		border-bottom: 0;
	}
	.lamp {
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: var(--fg-3);
	}
	[data-tone='ok'] .lamp {
		background: var(--success);
		box-shadow: 0 0 0 3px color-mix(in oklab, var(--success), transparent 82%);
	}
	[data-tone='warn'] .lamp {
		background: var(--warn);
	}
	[data-tone='bad'] .lamp {
		background: var(--danger);
		box-shadow: 0 0 0 3px color-mix(in oklab, var(--danger), transparent 78%);
		animation: pulse 1.6s var(--ease-in-out) infinite;
	}
	[data-tone='off'] .lamp {
		background: transparent;
		box-shadow: inset 0 0 0 2px var(--fg-3);
	}
	.id {
		min-width: 0;
	}
	.svc {
		font-family: var(--font-display);
		font-weight: 700;
		font-size: var(--text-base);
		color: var(--fg);
		text-decoration: none;
		margin-right: var(--space-2);
	}
	.svc:hover {
		color: var(--accent-text);
		text-decoration: underline;
	}
	.sub {
		margin-top: 2px;
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.state {
		display: grid;
		gap: 2px;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
	}
	[data-tone='bad'] .st {
		color: var(--danger-text);
	}
	[data-tone='off'] .st {
		color: var(--fg-quiet);
	}
	.restarts {
		font-size: var(--text-xs);
		color: var(--warn-text);
	}
	.res {
		display: grid;
		gap: 4px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-2);
		font-variant-numeric: tabular-nums;
	}
	.k {
		display: inline-block;
		width: 3.2em;
		color: var(--fg-quiet);
	}
	.mem {
		display: flex;
		align-items: center;
		gap: var(--space-2);
	}
	.bar {
		flex: 0 0 56px;
		height: 4px;
		border-radius: 2px;
		background: var(--surface-hover);
		overflow: hidden;
	}
	.bar span {
		display: block;
		height: 100%;
		background: var(--fg-3);
	}
	.ops {
		display: flex;
		gap: var(--space-2);
		justify-content: flex-end;
		flex-wrap: wrap;
	}
	.foot {
		margin-top: var(--space-3);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		max-width: 90ch;
	}
	@keyframes pulse {
		50% {
			box-shadow: 0 0 0 6px color-mix(in oklab, var(--danger), transparent 92%);
		}
	}
	@media (max-width: 1100px) {
		.rack li {
			grid-template-columns: 12px minmax(0, 1fr) minmax(0, 1fr);
			grid-template-areas: 'lamp id ops' 'lamp state res';
		}
		.lamp {
			grid-area: lamp;
			align-self: start;
			margin-top: 6px;
		}
		.id {
			grid-area: id;
		}
		.state {
			grid-area: state;
		}
		.res {
			grid-area: res;
		}
		.ops {
			grid-area: ops;
		}
	}
	@media (max-width: 640px) {
		.rack li {
			grid-template-columns: 12px minmax(0, 1fr);
			grid-template-areas: 'lamp id' 'lamp state' 'lamp res' 'lamp ops';
			gap: var(--space-2) var(--space-3);
		}
		.ops {
			justify-content: flex-start;
			margin-top: var(--space-1);
		}
	}
</style>
