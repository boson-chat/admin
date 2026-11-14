<script lang="ts">
	import type { Certificate, PublicProbe, ServiceHealth } from '$lib/types';
	import type { BrowserLatency } from '$lib/probe';

	let {
		services,
		certificates,
		edgeName = 'Traefik',
		ircConfigured = false,
		probes = [],
		browser = {}
	}: {
		services: ServiceHealth[];
		certificates: Certificate[];
		edgeName?: string;
		ircConfigured?: boolean;
		/** Public endpoints the browser times, by service name ("admin" = this panel, via the edge). */
		probes?: PublicProbe[];
		browser?: Record<string, BrowserLatency>;
	} = $props();

	let probeBy = $derived(Object.fromEntries(probes.map((p) => [p.name, p])));

	type You = { text: string; tip: string; na: boolean };
	function fromYou(name: string): You | null {
		const p = probeBy[name];
		if (!p) return null;
		const where = p.url.startsWith('/') ? 'this panel (' + p.url + ')' : p.url;
		const r = browser[name];
		if (!r) return { text: '…', tip: `Measuring from your browser to ${where}`, na: false };
		if (r.ms === null)
			return {
				text: 'n/a',
				tip: `Your browser couldn’t reach ${where} (${r.error ?? 'failed'}). The public endpoint may not exist yet.`,
				na: true
			};
		const how =
			p.kind === 'ws'
				? 'time to open a WebSocket (one connection, to stay under the IRC server’s connection throttle)'
				: `median of ${r.samples} requests after a warm-up`;
		return { text: ms(r.ms), tip: `From your browser to ${where}: ${how}.`, na: false };
	}
	function ms(v: number) {
		return (v < 10 ? v.toFixed(1) : v.toFixed(0)) + ' ms';
	}
	const internalTip =
		'Measured by the panel’s server over the internal network (the slowest passing check), so it leaves out the edge and your connection.';

	const statusWord: Record<string, string> = {
		up: 'Up',
		degraded: 'Degraded',
		down: 'Down',
		'': 'No checks'
	};

	// The edge (EDGE_NAME, Traefik in the stack) terminates TLS for every
	// hostname, so its health is the worst certificate.
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

	let hasIRC = $derived(ircConfigured || services.some((s) => /irc|ergo|anope/i.test(s.name)));
	const names: Record<string, string> = { irc: 'IRC' };

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
			<p>{edgeName}, TLS for every hostname</p>
			<p class="state">{edge.text}</p>
			{#if fromYou('admin')}
				{@const y = fromYou('admin')!}
				<p class="edge-lat" title={y.tip}>
					<span class="you" class:na={y.na}>{y.text}</span> <span class="lbl">from you to this panel</span>
				</p>
			{/if}
		</div>
	</div>

	<ul class="branches" aria-label="Services">
		{#each services as s (s.name)}
			{@const y = fromYou(s.name)}
			<li class="branch" data-tone={s.status || 'none'}>
				<span class="node" aria-hidden="true"></span>
				<div class="name">
					<h3>{names[s.name] ?? s.name}</h3>
					{#if s.version}<span class="version mono">{s.version}</span>{/if}
				</div>
				<span class="status">{statusWord[s.status]}</span>
				<span class="lat">
					{#if y}
						<span class="you" class:na={y.na} title={y.tip}>
							<span class="v">{y.text}</span> <span class="lbl">from you</span>
						</span>
					{/if}
					{#if latency(s)}
						<span class="int" title={internalTip}>
							<span class="v">{latency(s)}</span> <span class="lbl">internal</span>
						</span>
					{/if}
				</span>
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
				<span class="status muted">Not configured</span>
				<span class="hint muted">Set <code>IRC_CHECK</code></span>
			</li>
		{/if}
	</ul>
</div>

<style>
	.path {
		--track: var(--border-strong);
		display: grid;
		grid-template-columns: minmax(200px, 260px) 1fr;
		align-items: start;
		gap: 0;
		padding: var(--space-5) 0;
	}
	[data-tone='up'] {
		--tone: var(--success);
		--tone-text: var(--success-text);
	}
	[data-tone='degraded'] {
		--tone: var(--warn);
		--tone-text: var(--warn-text);
	}
	[data-tone='down'] {
		--tone: var(--danger);
		--tone-text: var(--danger-text);
	}
	[data-tone='none'] {
		--tone: var(--fg-3);
		--tone-text: var(--fg-quiet);
	}

	.node {
		width: 14px;
		height: 14px;
		border-radius: 50%;
		background: var(--tone);
		box-shadow: 0 0 0 4px color-mix(in oklab, var(--tone), transparent 82%);
		flex: none;
	}
	[data-tone='none'] > .node {
		background: var(--bg);
		border: 2px dashed var(--fg-3);
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
		background: var(--bg);
		padding-right: var(--space-3);
	}
	.edge p {
		color: var(--fg-quiet);
		font-size: var(--text-sm);
	}
	.edge .state {
		color: var(--tone-text);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		margin-top: var(--space-1);
	}
	h3 {
		font-size: var(--text-base);
		font-weight: 700;
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
		color: var(--fg-2);
		font-size: var(--text-xs);
		padding: 0 var(--space-1);
		border-radius: var(--radius-sm);
		background: var(--surface-hover);
	}
	.status {
		color: var(--tone-text);
		font-family: var(--font-mono);
		font-weight: 600;
		font-size: var(--text-xs);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
	}
	.lat {
		display: grid;
		justify-items: end;
		gap: 1px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		min-width: 9.5em;
		text-align: right;
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}
	.lat > span,
	.edge-lat {
		cursor: help;
	}
	.you .v,
	.edge-lat .you {
		color: var(--fg);
		font-weight: 600;
		font-size: var(--text-sm);
	}
	.you.na .v,
	.edge-lat .you.na {
		color: var(--fg-quiet);
		font-weight: 500;
	}
	.int {
		color: var(--fg-quiet);
	}
	.lbl {
		color: var(--fg-quiet);
		font-size: 10px;
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
	}
	.edge .edge-lat {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		margin-top: var(--space-1);
		font-variant-numeric: tabular-nums;
	}
	.hint {
		grid-column: 2 / -1;
		font-size: var(--text-xs);
		margin-top: var(--space-1);
	}
	.fails {
		grid-column: 2 / -1;
		list-style: none;
		margin: var(--space-2) 0 0;
		padding: var(--space-2) var(--space-3);
		border-left: 2px solid var(--danger);
		background: color-mix(in oklab, var(--danger), transparent 92%);
		color: var(--fg-2);
		font-size: var(--text-xs);
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
			box-shadow: 0 0 0 14px color-mix(in oklab, var(--tone), transparent 100%);
			transform: scale(0.4);
		}
		60% {
			box-shadow: 0 0 0 10px color-mix(in oklab, var(--tone), transparent 70%);
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
			grid-column: 2 / -1;
			grid-row: 2;
			display: flex;
			flex-wrap: wrap;
			gap: var(--space-1) var(--space-4);
			justify-items: start;
			min-width: 0;
			margin-top: var(--space-1);
		}
		.lat:empty {
			display: none;
		}
	}
</style>
