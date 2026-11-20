<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, isAbort } from '$lib/api';
	import { errorText } from '$lib/toasts.svelte';
	import { relative } from '$lib/format';
	import type { HostMetrics, MetricsRange } from '$lib/types';
	import {
		bytes,
		derive,
		duration,
		isRange,
		level,
		levelWord,
		loadNum,
		pct,
		ranges,
		refreshMs,
		targetLevel,
		targetSummary,
		usedPct,
		type Level,
		type ThresholdKey
	} from '$lib/hostmetrics';
	import TimeChart from '$lib/components/TimeChart.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';

	const initial = page.url.searchParams.get('range');
	let range = $state<MetricsRange>(isRange(initial) ? initial : '1h');
	let instance = $state(page.url.searchParams.get('instance') ?? '');
	let data = $state<HostMetrics | null>(null);
	let error = $state('');
	let loading = $state(false);
	let now = $state(Date.now());
	let ctrl: AbortController | null = null;

	async function load() {
		ctrl?.abort();
		const c = new AbortController();
		ctrl = c;
		loading = true;
		const qs = new URLSearchParams({ range });
		if (instance) qs.set('instance', instance);
		try {
			data = await api<HostMetrics>('GET', '/metrics/host?' + qs, undefined, undefined, c.signal);
			error = '';
		} catch (e) {
			if (isAbort(e)) return;
			error = errorText(e);
		} finally {
			if (ctrl === c) loading = false;
		}
	}

	function setRange(r: MetricsRange) {
		if (r === range) return;
		range = r;
		syncURL();
		load();
	}
	function setInstance(v: string) {
		instance = v;
		syncURL();
		load();
	}
	function syncURL() {
		const u = new URL(page.url);
		u.searchParams.set('range', range);
		if (instance) u.searchParams.set('instance', instance);
		else u.searchParams.delete('instance');
		goto(u.pathname + u.search, { replaceState: true, noScroll: true, keepFocus: true });
	}

	onMount(() => {
		load();
		const poll = setInterval(() => {
			if (document.visibilityState === 'visible') load();
		}, refreshMs);
		const tick = setInterval(() => (now = Date.now()), 5000);
		// Back on the tab after a while: refresh at once rather than wait.
		const onVis = () => {
			if (
				document.visibilityState === 'visible' &&
				data &&
				Date.now() - Date.parse(data.checked_at) > refreshMs
			)
				load();
		};
		document.addEventListener('visibilitychange', onVis);
		return () => {
			ctrl?.abort();
			clearInterval(poll);
			clearInterval(tick);
			document.removeEventListener('visibilitychange', onVis);
		};
	});

	let host = $derived(data?.host);
	let d = $derived(host ? derive(host.stats) : null);
	let root = $derived(host?.filesystems.find((f) => f.mountpoint === '/') ?? host?.filesystems[0]);
	let rootPct = $derived(root ? usedPct(root.size_bytes, root.avail_bytes) : null);
	let start = $derived(data ? Date.parse(data.start) / 1000 : 0);
	let end = $derived(data ? Date.parse(data.end) / 1000 : 0);

	interface Tile {
		key: string;
		label: string;
		value: string;
		detail: string;
		/** 0–100 for a meter; undefined = no meter. */
		meter?: number | null;
		lv: Level;
		word: string;
	}

	function tile(
		key: string,
		label: string,
		value: string,
		detail: string,
		th?: ThresholdKey,
		v?: number | null,
		meter?: number | null
	): Tile {
		const lv = th ? level(th, v) : 'ok';
		return { key, label, value, detail, meter, lv, word: th ? levelWord(th, lv) : '' };
	}

	let tiles = $derived.by((): Tile[] => {
		if (!host || !d) return [];
		const s = host.stats;
		const swapNone = s.swap_total_bytes === 0;
		return [
			tile('uptime', 'Uptime', duration(s.uptime_seconds), host.kernel ? `Linux ${host.kernel}` : ''),
			tile(
				'cpu',
				'CPU',
				pct(s.cpu_percent),
				s.cores ? `${s.cores} ${s.cores === 1 ? 'core' : 'cores'}, last minute` : 'last minute',
				'cpu',
				s.cpu_percent,
				s.cpu_percent
			),
			tile(
				'memory',
				'Memory used',
				pct(d.memUsedPct),
				d.memUsed !== null ? `${bytes(d.memUsed)} of ${bytes(s.mem_total_bytes)}` : '',
				'memory',
				d.memUsedPct,
				d.memUsedPct
			),
			swapNone
				? tile('swap', 'Swap used', 'None', 'No swap configured')
				: tile(
						'swap',
						'Swap used',
						pct(d.swapUsedPct),
						d.swapUsed !== null ? `${bytes(d.swapUsed)} of ${bytes(s.swap_total_bytes)}` : '',
						'swap',
						d.swapUsedPct,
						d.swapUsedPct
					),
			tile(
				'disk',
				root && root.mountpoint !== '/' ? `Disk ${root.mountpoint}` : 'Root disk used',
				pct(rootPct),
				root ? `${bytes(root.avail_bytes)} free of ${bytes(root.size_bytes)}` : '',
				'disk',
				rootPct,
				rootPct
			),
			tile(
				'load',
				'Load',
				loadNum(s.load1),
				s.cores
					? `${loadNum(s.load5)} · ${loadNum(s.load15)} (5, 15 min) on ${s.cores} ${s.cores === 1 ? 'core' : 'cores'}`
					: `${loadNum(s.load5)} · ${loadNum(s.load15)} (5, 15 min)`,
				'load',
				d.loadPerCore
			)
		];
	});

	let pressure = $derived.by(() => {
		const s = host?.stats;
		if (!s) return [];
		return [
			{ label: 'CPU', v: s.pressure_cpu },
			{ label: 'Memory', v: s.pressure_memory },
			{ label: 'I/O', v: s.pressure_io }
		].filter((p) => p.v !== null);
	});

	let summary = $derived.by(() => {
		if (!data) return '';
		if (!data.configured) return 'Metrics are off.';
		if (data.error) return 'The metrics backend didn’t answer.';
		if (!host) return targetSummary(data.targets);
		const bad = tiles
			.filter((t) => t.lv !== 'ok')
			.map((t) => `${t.label.toLowerCase()} ${t.word.toLowerCase()}`);
		const who = host.hostname ? `${host.hostname}: ` : '';
		return who + (bad.length ? bad.join(', ') + '.' : 'healthy.') + ' ' + targetSummary(data.targets);
	});

	const tagFor: Record<Level, string> = { ok: 'tag-ok', warn: 'tag-warn', bad: 'tag-bad' };
	const statusWord = { up: 'Up', down: 'Down', stale: 'Stale' } as const;
	let rangeLabel = $derived(
		{ '1h': 'last hour', '6h': 'last 6 hours', '24h': 'last 24 hours', '7d': 'last 7 days' }[range]
	);
</script>

<svelte:head><title>Metrics · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Metrics</h1>
		<p class="summary" aria-live="polite">
			{#if data}{summary}{:else if !error}Reading metrics…{/if}
		</p>
	</div>
	<div class="actions">
		{#if data}
			<span class="muted checked">Updated {relative(data.checked_at, now)}</span>
		{/if}
		{#if data && data.instances.length > 1}
			<label class="inst">
				<span class="sr-only">Host</span>
				<select value={data.instance} onchange={(e) => setInstance(e.currentTarget.value)}>
					{#each data.instances as i (i)}<option value={i}>{i}</option>{/each}
				</select>
			</label>
		{/if}
		<div class="seg" role="radiogroup" aria-label="Time range">
			{#each ranges as r (r.value)}
				<button
					type="button"
					role="radio"
					aria-checked={range === r.value}
					class:on={range === r.value}
					onclick={() => setRange(r.value)}>{r.label}</button
				>
			{/each}
		</div>
		<button class="btn btn-small" onclick={load} disabled={loading}>{loading ? 'Loading…' : 'Refresh'}</button
		>
	</div>
</div>

{#if error && !data}
	<State kind="error" title="Metrics didn’t load">
		{error}
		{#snippet action()}<button class="btn btn-small" onclick={load}>Try again</button>{/snippet}
	</State>
{:else if data}
	{#if error}<Notice tone="warn">The last refresh failed: {error}</Notice>{/if}

	{#if !data.configured}
		<State kind="off" title="Metrics are off">
			This is a deployment setting: point <code>ADMIN_METRICS_URL</code> in the stack’s <code>.env</code> at a
			Prometheus-compatible query API (VictoriaMetrics in the <code>metrics</code> profile:
			<code>http://victoriametrics:8428</code>) and run <code>docker compose up -d admin</code>.
		</State>
	{:else if data.error}
		<Notice tone="warn">The metrics backend didn’t answer: {data.error}</Notice>
	{:else}
		{#if !data.node_exporter}
			<State kind="off" title="No host metrics yet">
				Host metrics need node-exporter, which the stack’s <code>metrics</code> profile runs from the stack
				release that adds it (scrape job <code>node</code>). Update the stack, enable the metrics profile (<code
					>COMPOSE_PROFILES</code
				>
				includes <code>metrics</code>) and run <code>docker compose up -d</code>. The service health below
				works already.
			</State>
		{:else if host}
			<section aria-labelledby="now-h">
				<h2 id="now-h" class="sr-only">Now</h2>
				<div class="stats">
					{#each tiles as t (t.key)}
						<div class="stat {t.lv}">
							<span class="label"
								>{t.label}{#if t.word}<span class="tag {tagFor[t.lv]}">{t.word}</span>{/if}</span
							>
							<span class="value" class:nodata={t.value === '—'}>{t.value}</span>
							{#if t.meter !== undefined}
								<span class="meter" aria-hidden="true"
									><span style:width="{Math.min(100, Math.max(0, t.meter ?? 0))}%"></span></span
								>
							{/if}
							{#if t.detail}<span class="detail">{t.detail}</span>{/if}
						</div>
					{/each}
				</div>
				{#if pressure.length || host.stats.disk_busy_percent !== null}
					<p class="psi">
						{#if pressure.length}
							<span title="Pressure stall information: the share of time some tasks waited for the resource"
								>Waiting (PSI)</span
							>
							{#each pressure as p (p.label)}
								<span class:warnv={level('pressure', p.v) !== 'ok'}>{p.label} {pct(p.v)}</span>
							{/each}
						{/if}
						{#if host.stats.disk_busy_percent !== null}
							<span>Disk busy {pct(host.stats.disk_busy_percent)}</span>
						{/if}
					</p>
				{/if}
			</section>

			<section class="section" aria-labelledby="charts-h">
				<div class="section-head">
					<h2 id="charts-h">Over the {rangeLabel}</h2>
					<p>
						Every {Math.round(data.step) >= 60 ? `${Math.round(data.step / 60)} min` : `${data.step} s`};
						refreshes every 30 s while this tab is open.
					</p>
				</div>
				<div class="charts">
					{#each host.charts as c (c.key)}
						<TimeChart chart={c} {start} {end} step={data.step} stale={loading} />
					{/each}
				</div>
			</section>

			{#if host.filesystems.length}
				<section class="section" aria-labelledby="fs-h">
					<div class="section-head"><h2 id="fs-h">Filesystems</h2></div>
					<div class="table-wrap">
						<table>
							<thead>
								<tr>
									<th>Mount</th>
									<th class="hide-s">Device</th>
									<th class="num">Size</th>
									<th class="num">Free</th>
									<th class="num">Used</th>
								</tr>
							</thead>
							<tbody>
								{#each host.filesystems as f (f.mountpoint)}
									{@const u = usedPct(f.size_bytes, f.avail_bytes)}
									{@const lv = level('disk', u)}
									<tr>
										<td class="mono">{f.mountpoint}</td>
										<td class="mono muted hide-s">{f.device}{f.fstype ? ` · ${f.fstype}` : ''}</td>
										<td class="num">{bytes(f.size_bytes)}</td>
										<td class="num">{bytes(f.avail_bytes)}</td>
										<td class="num">
											{#if lv !== 'ok'}<span class="tag {tagFor[lv]}">{levelWord('disk', lv)}</span>{/if}
											{pct(u)}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				</section>
			{/if}
		{/if}

		<section class="section" aria-labelledby="tg-h">
			<div class="section-head">
				<h2 id="tg-h">Service health</h2>
				<p>Each scrape target of the metrics backend; availability over the {rangeLabel}.</p>
			</div>
			{#if data.targets_error}
				<Notice tone="warn">Couldn’t read the scrape targets: {data.targets_error}</Notice>
			{:else if !data.targets.length}
				<State kind="empty" title="No scrape targets"
					>The metrics backend reports no <code>up</code> series.</State
				>
			{:else}
				<div class="table-wrap">
					<table>
						<thead>
							<tr>
								<th>Job</th>
								<th>State</th>
								<th class="hide-s">Instance</th>
								<th class="hide-s">Last scrape</th>
								<th class="num">Available</th>
							</tr>
						</thead>
						<tbody>
							{#each data.targets as t (t.job + t.instance)}
								<tr>
									<td class="mono">{t.job}</td>
									<td>
										<span class="tag {tagFor[targetLevel(t)]}"
											><span class="dot" style:background="currentColor"></span>{statusWord[t.status]}</span
										>
										{#if t.status !== 'up'}
											<span class="lastup muted"
												>{#if t.last_up}last up <Time at={t.last_up} compact />{:else}not up in this range{/if}</span
											>
										{/if}
									</td>
									<td class="mono muted hide-s">{t.instance}</td>
									<td class="hide-s"><Time at={t.last_scrape} compact /></td>
									<td class="num">{pct(t.availability)}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</section>
	{/if}
{:else}
	<State kind="loading" title="Reading metrics…" />
{/if}

<style>
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
	/* range selector: a segmented control */
	.seg {
		display: inline-flex;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
		padding: 2px;
		gap: 2px;
	}
	.seg button {
		font: inherit;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		min-width: 44px;
		padding: 4px var(--space-2);
		border: 0;
		border-radius: var(--radius-sm);
		background: transparent;
		color: var(--fg-2);
		cursor: pointer;
	}
	.seg button:hover {
		background: var(--surface-hover);
		color: var(--fg);
	}
	.seg button.on {
		background: var(--accent-dim);
		color: var(--accent-text);
		font-weight: 600;
	}
	.seg button:focus-visible {
		outline: none;
		box-shadow: var(--focus-ring);
	}
	.inst select {
		font: inherit;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		padding: 5px var(--space-2);
		background: var(--surface);
		color: var(--fg);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}
	/* stat tiles: the Stack page's hairline grid */
	.stats {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
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
		min-height: 120px;
	}
	.stat.warn {
		box-shadow: inset 0 2px 0 var(--warn);
	}
	.stat.bad {
		box-shadow: inset 0 2px 0 var(--danger);
	}
	.label {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-2);
		color: var(--fg-2);
		font-size: var(--text-sm);
	}
	.value {
		font-family: var(--font-mono);
		font-size: var(--text-3xl);
		font-weight: 600;
		letter-spacing: -0.03em;
		line-height: 1.1;
	}
	.value.nodata {
		color: var(--fg-3);
	}
	.meter {
		display: block;
		height: 4px;
		border-radius: 2px;
		background: var(--surface-hover);
		overflow: hidden;
		margin-top: var(--space-1);
	}
	.meter span {
		display: block;
		height: 100%;
		border-radius: 2px;
		background: var(--series-1);
	}
	.stat.warn .meter span {
		background: var(--warn);
	}
	.stat.bad .meter span {
		background: var(--danger);
	}
	.detail {
		color: var(--fg-quiet);
		font-size: var(--text-xs);
		font-family: var(--font-mono);
	}
	.psi {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-4);
		margin: var(--space-3) 0 0;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-2);
	}
	.psi > span:first-child {
		color: var(--fg-quiet);
	}
	.psi .warnv {
		color: var(--warn-text);
	}
	.charts {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(min(100%, 420px), 1fr));
		gap: var(--space-4);
	}
	td.num {
		white-space: nowrap;
	}
	.lastup {
		font-size: var(--text-xs);
		margin-left: var(--space-2);
		white-space: nowrap;
	}
	td .tag + :global(*) {
		margin-left: var(--space-1);
	}
	@media (min-width: 1500px) {
		.stats {
			grid-template-columns: repeat(6, minmax(0, 1fr));
		}
	}
	@media (max-width: 640px) {
		.hide-s {
			display: none;
		}
		.value {
			font-size: var(--text-2xl);
		}
		.stats {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
		.stat {
			min-height: 0;
			padding: var(--space-3);
		}
	}
</style>
