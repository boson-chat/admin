<script lang="ts">
	import type { Certificate, ServiceHealth } from '$lib/types';

	let { services, certificates }: { services: ServiceHealth[]; certificates: Certificate[] } = $props();

	const statusWord: Record<string, string> = {
		up: 'Up',
		degraded: 'Degraded',
		down: 'Down',
		'': 'No checks'
	};

	// The edge: Caddy terminates TLS for every hostname, so its health is the
	// worst certificate.
	let edge = $derived.by(() => {
		if (certificates.length === 0) return { tone: 'none', text: 'No hostnames configured' };
		const bad = certificates.filter((c) => c.status === 'error' || c.status === 'expired').length;
		const soon = certificates.filter((c) => c.status === 'warning' || c.status === 'critical').length;
		if (bad) return { tone: 'down', text: `${bad} of ${certificates.length} certificates failing` };
		if (soon)
			return { tone: 'degraded', text: `${soon} of ${certificates.length} certificates expiring soon` };
		return {
			tone: 'up',
			text: certificates.length === 1 ? 'Certificate valid' : `All ${certificates.length} certificates valid`
		};
	});

	let hasIRC = $derived(services.some((s) => /irc|ergo|anope/i.test(s.name)));

	function latency(s: ServiceHealth): string {
		const ok = s.checks.filter((c) => c.ok);
		if (!ok.length) return '';
		return Math.max(...ok.map((c) => c.latency_ms)).toFixed(0) + ' ms';
	}
	function failing(s: ServiceHealth) {
		return s.checks.filter((c) => !c.ok);
	}
</script>

<div class="path">
	<div class="edge" data-tone={edge.tone}>
		<span class="node" aria-hidden="true"></span>
		<div>
			<h3>Edge</h3>
			<p>Caddy, TLS for every hostname</p>
			<p class="state">{edge.text}</p>
		</div>
	</div>

	<ul class="branches" aria-label="Services">
		{#each services as s (s.name)}
			<li class="branch" data-tone={s.status || 'none'}>
				<span class="node" aria-hidden="true"></span>
				<div class="name">
					<h3>{s.name}</h3>
					{#if s.version}<span class="version mono">{s.version}</span>{/if}
				</div>
				<span class="status">{statusWord[s.status]}</span>
				<span class="lat muted">{latency(s)}</span>
				{#if failing(s).length}
					<ul class="fails">
						{#each failing(s) as c (c.url)}
							<li>
								<span class="mono">{c.url}</span>
								<span>{c.status_code ? `HTTP ${c.status_code}` : ''} {c.detail ?? ''}</span>
							</li>
						{/each}
					</ul>
				{/if}
			</li>
		{:else}
			<li class="branch" data-tone="none">
				<span class="node" aria-hidden="true"></span>
				<div class="name"><h3>No health checks</h3></div>
				<span class="status muted">Set HEALTH_TARGETS</span>
			</li>
		{/each}
		{#if !hasIRC}
			<li class="branch pending" data-tone="none">
				<span class="node" aria-hidden="true"></span>
				<div class="name"><h3>IRC</h3></div>
				<span class="status muted">Not monitored here yet</span>
			</li>
		{/if}
	</ul>
</div>

<style>
	.path {
		--track: var(--line-strong);
		display: grid;
		grid-template-columns: minmax(200px, 260px) 1fr;
		align-items: start;
		gap: 0;
		padding: var(--space-5) 0;
	}
	[data-tone='up'] {
		--tone: var(--ok);
	}
	[data-tone='degraded'] {
		--tone: var(--warn);
	}
	[data-tone='down'] {
		--tone: var(--bad);
	}
	[data-tone='none'] {
		--tone: var(--muted);
	}

	.node {
		width: 14px;
		height: 14px;
		border-radius: 50%;
		background: var(--tone);
		box-shadow: 0 0 0 4px color-mix(in srgb, var(--tone) 18%, transparent);
		flex: none;
	}
	[data-tone='none'] > .node {
		background: var(--ink);
		border: 2px dashed var(--muted);
		box-shadow: none;
	}

	.edge {
		position: relative;
		isolation: isolate;
		display: flex;
		gap: var(--space-3);
		align-items: flex-start;
		padding-right: var(--space-5);
	}
	.edge .node {
		margin-top: 3px;
	}
	/* track from the edge node to the bus */
	.edge::after {
		content: '';
		position: absolute;
		left: 26px;
		right: 0;
		top: 9px;
		height: 2px;
		background: var(--track);
		z-index: -1;
	}
	.edge > div {
		background: var(--ink);
		padding-right: var(--space-3);
	}
	.edge p {
		color: var(--muted);
		font-size: var(--t-sm);
	}
	.edge .state {
		color: var(--tone);
		margin-top: var(--space-1);
	}

	.branches {
		list-style: none;
		margin: 0;
		padding: 0;
		position: relative;
	}
	.branch {
		position: relative;
		display: grid;
		grid-template-columns: 14px minmax(0, 1fr) auto auto;
		align-items: center;
		column-gap: var(--space-3);
		padding: 0 0 var(--space-5) var(--space-6);
	}
	/* the bus: a vertical track joining every branch */
	.branch::before {
		content: '';
		position: absolute;
		left: 0;
		top: 0;
		bottom: 0;
		width: 2px;
		background: var(--track);
	}
	.branch:first-child::before {
		top: 9px;
	}
	.branch:last-child::before {
		bottom: auto;
		height: 10px;
	}
	.branch:first-child:last-child::before {
		display: none;
	}
	/* stub from the bus to the node */
	.branch::after {
		content: '';
		position: absolute;
		left: 0;
		top: 9px;
		width: var(--space-6);
		height: 2px;
		background: var(--track);
	}
	.branch.pending::after,
	.branch.pending::before {
		background: repeating-linear-gradient(90deg, var(--track) 0 4px, transparent 4px 8px);
	}
	.branch.pending::before {
		background: repeating-linear-gradient(180deg, var(--track) 0 4px, transparent 4px 8px);
	}
	.branch .node {
		grid-row: 1;
		align-self: start;
		margin-top: 3px;
	}
	.name {
		display: flex;
		align-items: baseline;
		gap: var(--space-3);
		min-width: 0;
		flex-wrap: wrap;
	}
	.version {
		color: var(--text-2);
		font-size: var(--t-xs);
	}
	.status {
		color: var(--tone);
		font-weight: 600;
		font-size: var(--t-sm);
	}
	.lat {
		font-size: var(--t-xs);
		min-width: 3.5em;
		text-align: right;
	}
	.fails {
		grid-column: 2 / -1;
		list-style: none;
		margin: var(--space-2) 0 0;
		padding: var(--space-2) var(--space-3);
		border-left: 2px solid var(--bad);
		background: var(--bad-soft);
		font-size: var(--t-xs);
		display: grid;
		gap: var(--space-1);
	}
	.fails li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		overflow-wrap: anywhere;
	}

	/* one orchestrated moment: the service nodes light up in turn on first paint */
	@media (prefers-reduced-motion: no-preference) {
		.branch .node {
			animation: arrive 700ms cubic-bezier(0.2, 0.7, 0.2, 1) both;
		}
		.branch:nth-child(2) .node {
			animation-delay: 90ms;
		}
		.branch:nth-child(3) .node {
			animation-delay: 180ms;
		}
		.branch:nth-child(4) .node {
			animation-delay: 270ms;
		}
		.branch:nth-child(n + 5) .node {
			animation-delay: 360ms;
		}
	}
	@keyframes arrive {
		from {
			box-shadow: 0 0 0 14px color-mix(in srgb, var(--tone) 0%, transparent);
			transform: scale(0.4);
		}
		60% {
			box-shadow: 0 0 0 10px color-mix(in srgb, var(--tone) 30%, transparent);
		}
	}

	@media (max-width: 720px) {
		.path {
			grid-template-columns: 1fr;
		}
		.edge {
			padding: 0 0 var(--space-5);
		}
		.edge::after {
			left: 6px;
			right: auto;
			top: 22px;
			bottom: 0;
			width: 2px;
			height: auto;
		}
		.branches {
			margin-left: 6px;
		}
		.branch {
			grid-template-columns: 14px minmax(0, 1fr) auto;
			padding-left: var(--space-5);
		}
		.branch::after {
			width: var(--space-5);
		}
		.branch:first-child::before {
			top: 0;
		}
		.lat {
			display: none;
		}
	}
</style>
