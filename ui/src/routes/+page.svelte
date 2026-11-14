<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { errorText } from '$lib/toasts.svelte';
	import { date, num, relative } from '$lib/format';
	import type { Certificate, Overview, Stat } from '$lib/types';
	import SignalPath from '$lib/components/SignalPath.svelte';
	import Sparkline from '$lib/components/Sparkline.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import State from '$lib/components/State.svelte';
	import { due, measure, type BrowserLatency } from '$lib/probe';

	let data = $state<Overview | null>(null);
	let error = $state('');
	let loading = $state(false);
	let now = $state(Date.now());
	// Latency from this browser to each public endpoint, by service name.
	let browser = $state<Record<string, BrowserLatency>>({});
	let probing = false;

	async function load(forced = false) {
		loading = true;
		try {
			data = await api<Overview>('GET', '/overview');
			error = '';
			probe(forced);
		} catch (e) {
			error = errorText(e);
		} finally {
			loading = false;
		}
	}

	async function probe(forced: boolean) {
		if (probing || !data?.public_probes?.length) return;
		const todo = due(data.public_probes, browser, Date.now(), forced);
		if (!todo.length) return;
		probing = true;
		try {
			await Promise.all(
				todo.map(async (p) => {
					browser[p.name] = await measure(p);
				})
			);
		} finally {
			probing = false;
		}
	}

	onMount(() => {
		load();
		const poll = setInterval(() => {
			if (document.visibilityState === 'visible') load();
		}, 15000);
		const tick = setInterval(() => (now = Date.now()), 5000);
		return () => {
			clearInterval(poll);
			clearInterval(tick);
		};
	});

	let failing = $derived(
		(data?.services ?? []).filter((x) => x.status === 'down' || x.status === 'degraded').map((x) => x.name)
	);

	let summary = $derived.by(() => {
		if (!data) return '';
		const s = data.services.filter((x) => x.status);
		const down = s.filter((x) => x.status === 'down').map((x) => x.name);
		const degraded = s.filter((x) => x.status === 'degraded').map((x) => x.name);
		if (down.length) return `${down.join(', ')} ${down.length === 1 ? 'is' : 'are'} down.`;
		if (degraded.length)
			return `${degraded.join(', ')} ${degraded.length === 1 ? 'is' : 'are'} partly failing.`;
		if (!s.length) return 'No health checks are configured.';
		const up = s.length === 1 ? 'The one checked service is up' : `All ${s.length} checked services are up`;
		const badCerts = data.certificates.filter((c) => c.status !== 'ok').length;
		if (badCerts)
			return `${up}, but ${badCerts} ${badCerts === 1 ? 'certificate needs' : 'certificates need'} attention.`;
		return up + '.';
	});

	const certWord: Record<Certificate['status'], string> = {
		ok: 'Valid',
		warning: 'Renews soon',
		critical: 'Expiring',
		expired: 'Expired',
		error: 'Problem'
	};
	const certTag: Record<Certificate['status'], string> = {
		ok: 'tag-ok',
		warning: 'tag-warn',
		critical: 'tag-bad',
		expired: 'tag-bad',
		error: 'tag-bad'
	};

	function days(c: Certificate) {
		if (c.days_left === undefined) return '';
		const d = Math.floor(c.days_left);
		return d < 0 ? `${-d} days ago` : d === 0 ? 'today' : `in ${d} days`;
	}

	let groups = $derived.by(() => {
		const st = data?.metrics.stats ?? [];
		return [
			{ key: 'voice', title: 'Voice', stats: st.filter((s) => s.group === 'voice') },
			{ key: 'auth', title: 'Sign-ins', stats: st.filter((s) => s.group === 'auth') }
		];
	});

	function grafana(path = '') {
		return data?.grafana_url ? data.grafana_url + path : '';
	}
	function unit(s: Stat) {
		return s.unit ?? '';
	}
</script>

<svelte:head><title>Stack · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Stack</h1>
		<p class="summary" aria-live="polite">
			{#if data}{summary}{:else if !error}Checking services…{/if}
		</p>
	</div>
	<div class="actions">
		{#if data}
			<span class="muted checked">Checked {relative(data.checked_at, now)}</span>
		{/if}
		<button class="btn btn-small" onclick={() => load(true)} disabled={loading}
			>{loading ? 'Checking…' : 'Check now'}</button
		>
		<a class="btn btn-small" href="/services"
			>{session.info?.features.services ? 'Restart, stop, logs' : 'Services'}</a
		>
		{#if grafana()}
			<a class="btn btn-small" href={grafana()} target="_blank" rel="noopener noreferrer">Open Grafana</a>
		{/if}
	</div>
</div>

{#if error}
	<Notice>{error}</Notice>
{/if}

{#if data}
	<section aria-labelledby="svc-h">
		<h2 id="svc-h" class="sr-only">Services</h2>
		<SignalPath
			services={data.services}
			certificates={data.certificates}
			edgeName={data.edge?.name || 'Traefik'}
			ircConfigured={data.irc?.configured ?? false}
			probes={data.public_probes ?? []}
			{browser}
		/>
		{#if failing.length}
			<p class="fix">
				{failing.join(', ')}
				{failing.length === 1 ? 'is' : 'are'} failing.
				{#if session.info?.features.services}
					<a href="/services">Restart {failing.length === 1 ? 'it' : 'them'} or read the logs</a>
				{:else}
					<a href="/services">Services</a> can restart it once the Docker socket proxy is set up.
				{/if}
			</p>
		{/if}
	</section>

	<section class="section" aria-labelledby="cert-h">
		<div class="section-head">
			<h2 id="cert-h">Certificates</h2>
			<p>Checked by connecting to each host, every 5 minutes.</p>
		</div>
		{#if data.certificates.length === 0}
			<Notice tone="info">
				No hostnames are checked. Set <code>TLS_CHECK_HOSTS</code>, for example
				<code>irc.example.com:6697,voice.example.com:443</code>.
			</Notice>
		{:else}
			<div class="table-wrap">
				<table>
					<thead>
						<tr>
							<th>Host</th>
							<th>Status</th>
							<th>Expires</th>
							<th class="hide-s">Issuer</th>
						</tr>
					</thead>
					<tbody>
						{#each data.certificates as c (c.target)}
							<tr>
								<td>
									<span class="mono">{c.target}</span>
									{#if c.address !== c.target}<div class="muted small">via {c.address}</div>{/if}
								</td>
								<td>
									<span class="tag {certTag[c.status]}">{certWord[c.status]}</span>
									{#if c.error}<div class="small err">{c.error}</div>{/if}
								</td>
								<td>
									{#if c.cert}
										{date(c.cert.not_after)} <span class="muted">({days(c)})</span>
									{:else}
										<span class="muted">—</span>
									{/if}
								</td>
								<td class="hide-s">{c.cert?.issuer ?? ''}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>

	<section class="section" aria-labelledby="met-h">
		<div class="section-head">
			<h2 id="met-h">Live numbers</h2>
			{#if data.metrics.configured}<p>From the metrics backend; trends cover the last hour.</p>{/if}
		</div>
		{#if !data.metrics.configured}
			<Notice tone="info">
				Live metrics are off. Point <code>METRICS_URL</code> at a Prometheus-compatible query API (for example
				VictoriaMetrics at <code>http://victoriametrics:8428</code>) to see voice and sign-in numbers here.
			</Notice>
		{:else if data.metrics.error}
			<Notice tone="warn">The metrics backend didn’t answer: {data.metrics.error}</Notice>
		{:else}
			{#each groups as g (g.key)}
				<h3 class="group">{g.title}</h3>
				<div class="stats">
					{#each g.stats as s (s.key)}
						<div class="stat" title={s.query}>
							<span class="label">{s.label}</span>
							<span class="value" class:nodata={s.value === null}>
								{num(s.value)}{#if s.value !== null && unit(s)}<small>{unit(s)}</small>{/if}
							</span>
							{#if s.error}
								<span class="small err">{s.error}</span>
							{:else if s.value === null}
								<span class="small muted">No data yet</span>
							{/if}
							{#if s.breakdown?.length}
								<ul class="parts">
									{#each s.breakdown as p (p.label)}
										<li class:bad={p.label !== 'success'}>
											<span>{p.label.replaceAll('_', ' ')}</span><span>{num(p.value)}</span>
										</li>
									{/each}
								</ul>
							{/if}
							{#if s.trend?.length}<Sparkline points={s.trend} label={s.label} />{/if}
						</div>
					{/each}
				</div>
			{/each}
		{/if}
	</section>
{:else if !error}
	<State kind="loading" title="Checking services…" />
{/if}

<style>
	.fix {
		margin-top: var(--space-3);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--danger-text);
	}
	.summary {
		font-size: var(--text-lg);
		color: var(--fg);
	}
	.actions {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		flex-wrap: wrap;
	}
	.checked {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		margin-right: var(--space-2);
	}
	.small {
		font-size: var(--text-xs);
	}
	.err {
		color: var(--danger-text);
		font-family: var(--font-mono);
		margin-top: 2px;
	}
	.group {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		color: var(--fg-quiet);
		margin: var(--space-6) 0 var(--space-2);
	}
	/* stat tiles: hairline grid of raised cells, like the client's server list */
	.stats {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
		/* tiles overlap by 1px so neighbours share one hairline */
		padding: 1px 0 0 1px;
	}
	.stat {
		display: grid;
		align-content: start;
		gap: var(--space-1);
		padding: var(--space-4);
		background: var(--surface);
		border: 1px solid var(--border);
		margin: -1px 0 0 -1px;
		min-height: 128px;
	}
	.label {
		color: var(--fg-2);
		font-size: var(--text-sm);
	}
	.value {
		font-family: var(--font-mono);
		font-size: var(--text-3xl);
		font-weight: 600;
		letter-spacing: -0.03em;
		line-height: 1.1;
		font-variant-numeric: tabular-nums;
	}
	.value small {
		font-size: var(--text-sm);
		font-weight: 500;
		letter-spacing: 0;
		color: var(--fg-quiet);
		margin-left: 2px;
	}
	.value.nodata {
		color: var(--fg-3);
	}
	.parts {
		list-style: none;
		margin: var(--space-1) 0 0;
		padding: 0;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		display: grid;
		gap: 2px;
	}
	.parts li {
		display: flex;
		justify-content: space-between;
		color: var(--fg-2);
	}
	.parts li.bad {
		color: var(--warn-text);
	}
	@media (max-width: 640px) {
		.hide-s {
			display: none;
		}
	}
</style>
