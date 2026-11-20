<script lang="ts">
	import type { HostChart } from '$lib/types';
	import {
		axis,
		dataMax,
		formatUnit,
		latest,
		nearest,
		sample,
		segments,
		stack,
		tickLabel,
		timestamps,
		tooltipTime,
		valueAt,
		xTickLabel,
		xTicks
	} from '$lib/hostmetrics';

	/**
	 * A time-series chart in inline SVG: up to four series in the fixed
	 * categorical order, recessive grid, legend with the newest values, a
	 * crosshair tooltip (pointer and arrow keys) and a table view.
	 */
	let {
		chart,
		start,
		end,
		step,
		/** Dims the chart while a refetch is in flight (keeps the frame). */
		stale = false
	}: { chart: HostChart; start: number; end: number; step: number; stale?: boolean } = $props();

	const H = 180;
	const pad = { t: 10, r: 10, b: 24, l: 48 };

	let width = $state(0);
	let hover = $state<number | null>(null); // index into ts

	let w = $derived(Math.max(width, 200));
	let pw = $derived(w - pad.l - pad.r);
	let ph = H - pad.t - pad.b;

	let ts = $derived(timestamps(chart.series));
	let stacked = $derived(chart.stacked ? stack(chart.series) : []);
	let ax = $derived(
		axis(
			Math.max(dataMax(chart.series, !!chart.stacked), (chart.reference?.value ?? 0) * 1.15),
			chart.unit,
			chart.max
		)
	);
	let ymax = $derived(ax.max);
	let ticks = $derived(ax.ticks);
	let xt = $derived(xTicks(start, end, Math.max(2, Math.floor(pw / 90))));

	const x = (t: number) => pad.l + ((t - start) / Math.max(end - start, 1)) * pw;
	const y = (v: number) => pad.t + ph - (Math.min(Math.max(v, 0), ymax) / ymax) * ph;

	const color = (i: number) => `var(--series-${(i % 4) + 1})`;

	function linePath(pts: [number, number][]): string {
		return pts.map((p, i) => `${i ? 'L' : 'M'}${x(p[0]).toFixed(1)},${y(p[1]).toFixed(1)}`).join('');
	}
	function bandPath(band: [number, number, number][]): string {
		if (!band.length) return '';
		const top = band.map((p, i) => `${i ? 'L' : 'M'}${x(p[0]).toFixed(1)},${y(p[2]).toFixed(1)}`).join('');
		const bottom = band
			.slice()
			.reverse()
			.map((p) => `L${x(p[0]).toFixed(1)},${y(p[1]).toFixed(1)}`)
			.join('');
		return top + bottom + 'Z';
	}
	function topLine(band: [number, number, number][]): string {
		return band.map((p, i) => `${i ? 'L' : 'M'}${x(p[0]).toFixed(1)},${y(p[2]).toFixed(1)}`).join('');
	}

	let hoverT = $derived(hover !== null && hover < ts.length ? ts[hover] : null);
	/** Rows of the tooltip: every series at the crosshair. */
	let readout = $derived.by(() => {
		if (hoverT === null) return [];
		return chart.series.map((s, i) => {
			const v = valueAt(s, hoverT);
			const yv = chart.stacked ? (stacked[i]?.find((p) => p[0] === hoverT)?.[2] ?? null) : v;
			return { name: s.name, v, yv, i };
		});
	});
	let total = $derived(chart.stacked && readout.length ? readout.reduce((a, r) => a + (r.v ?? 0), 0) : null);

	function onMove(e: PointerEvent) {
		const svg = e.currentTarget as SVGSVGElement;
		const r = svg.getBoundingClientRect();
		const px = ((e.clientX - r.left) / r.width) * w;
		if (px < pad.l - 8 || px > w - pad.r + 8 || !ts.length) {
			hover = null;
			return;
		}
		const t = start + ((px - pad.l) / pw) * (end - start);
		hover = nearest(ts, t);
	}
	function onKey(e: KeyboardEvent) {
		if (!ts.length) return;
		const cur = hover ?? ts.length - 1;
		if (e.key === 'ArrowLeft') hover = Math.max(0, cur - 1);
		else if (e.key === 'ArrowRight') hover = Math.min(ts.length - 1, cur + 1);
		else if (e.key === 'Home') hover = 0;
		else if (e.key === 'End') hover = ts.length - 1;
		else if (e.key === 'Escape') hover = null;
		else return;
		e.preventDefault();
	}

	let tipLeft = $derived(hoverT === null ? 0 : x(hoverT));
	let tipFlip = $derived(tipLeft > w * 0.55);

	let tableRows = $derived(sample(ts, 12).reverse());
	let summary = $derived(
		chart.series.map((s) => `${s.name} ${formatUnit(chart.unit, latest(s))}`).join(', ')
	);
</script>

<figure class="chart" class:stale>
	<figcaption>
		<h3>{chart.title}</h3>
		{#if chart.series.length}
			<ul class="legend" aria-label="Series, newest value">
				{#each chart.series as s, i (s.name)}
					<li>
						<span class="key" class:area={chart.stacked} style:--c={color(i)} aria-hidden="true"></span>
						<span class="name">{s.name}</span>
						<span class="val">{formatUnit(chart.unit, latest(s))}</span>
					</li>
				{/each}
			</ul>
		{/if}
	</figcaption>

	<div class="plot" bind:clientWidth={width}>
		{#if chart.error}
			<p class="empty err" style:height="{H}px">This chart didn’t load: {chart.error}</p>
		{:else if !chart.series.length}
			<p class="empty" style:height="{H}px">No data in this range.</p>
		{:else if width}
			<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
			<svg
				viewBox="0 0 {w} {H}"
				width={w}
				height={H}
				role="img"
				aria-label="{chart.title}: {summary}. Use the arrow keys to read values."
				tabindex="0"
				onpointermove={onMove}
				onpointerleave={() => (hover = null)}
				onkeydown={onKey}
				onblur={() => (hover = null)}
			>
				<g class="grid">
					{#each ticks as v (v)}
						<line x1={pad.l} x2={w - pad.r} y1={y(v)} y2={y(v)} class:base={v === 0} />
						<text x={pad.l - 6} y={y(v)} dy="0.32em" text-anchor="end">{tickLabel(chart.unit, v)}</text>
					{/each}
					{#each xt as t (t)}
						<text x={x(t)} y={H - 6} text-anchor="middle">{xTickLabel(t, end - start)}</text>
					{/each}
				</g>

				{#if chart.stacked}
					{#each stacked as band, i (i)}
						<path d={bandPath(band)} class="band" style:--c={color(i)} />
					{/each}
					<!-- 2px surface gaps between the bands -->
					{#each stacked.slice(0, -1) as band, i (i)}
						<path d={topLine(band)} class="gap" />
					{/each}
				{:else}
					{#each chart.series as s, i (s.name)}
						{#each segments(s.points, step) as seg, j (j)}
							{#if seg.length === 1}
								<circle cx={x(seg[0][0])} cy={y(seg[0][1])} r="2" style:fill={color(i)} />
							{:else}
								<path d={linePath(seg)} class="line" style:--c={color(i)} />
							{/if}
						{/each}
					{/each}
				{/if}

				{#if chart.reference && chart.reference.value <= ymax}
					<g class="ref">
						<line x1={pad.l} x2={w - pad.r} y1={y(chart.reference.value)} y2={y(chart.reference.value)} />
						<text x={w - pad.r} y={y(chart.reference.value) - 4} text-anchor="end"
							>{chart.reference.value} {chart.reference.label}</text
						>
					</g>
				{/if}

				{#if hoverT !== null}
					<line class="cross" x1={x(hoverT)} x2={x(hoverT)} y1={pad.t} y2={pad.t + ph} />
					{#each readout as r (r.name)}
						{#if r.yv !== null}
							<circle class="dot" cx={x(hoverT)} cy={y(r.yv)} r="4" style:--c={color(r.i)} />
						{/if}
					{/each}
				{/if}
			</svg>
			{#if hoverT !== null}
				<div class="tip" class:flip={tipFlip} style:left="{tipLeft}px" role="status">
					<p class="when">{tooltipTime(hoverT)}</p>
					{#each readout as r (r.name)}
						<p class="row">
							<span class="tkey" style:--c={color(r.i)} aria-hidden="true"></span>
							<strong>{formatUnit(chart.unit, r.v)}</strong>
							<span>{r.name}</span>
						</p>
					{/each}
					{#if total !== null}
						<p class="row total"><strong>{formatUnit(chart.unit, total)}</strong><span>total</span></p>
					{/if}
				</div>
			{/if}
		{/if}
	</div>

	{#if chart.series.length && !chart.error}
		<details class="table">
			<summary>Table</summary>
			<div class="table-wrap">
				<table>
					<thead>
						<tr>
							<th>Time</th>
							{#each chart.series as s (s.name)}<th class="num">{s.name}</th>{/each}
						</tr>
					</thead>
					<tbody>
						{#each tableRows as t (t)}
							<tr>
								<td class="mono">{tooltipTime(t)}</td>
								{#each chart.series as s (s.name)}
									<td class="num">{formatUnit(chart.unit, valueAt(s, t))}</td>
								{/each}
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</details>
	{/if}
</figure>

<style>
	.chart {
		margin: 0;
		padding: var(--space-4);
		background: var(--surface);
		border: 1px solid var(--border);
		min-width: 0;
		transition: opacity var(--motion-base) var(--ease-standard);
	}
	.chart.stale {
		opacity: 0.6;
	}
	figcaption {
		display: grid;
		gap: var(--space-2);
		margin-bottom: var(--space-3);
	}
	h3 {
		font-size: var(--text-base);
		font-weight: 600;
	}
	.legend {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-4);
		font-size: var(--text-sm);
	}
	.legend li {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		white-space: nowrap;
	}
	.key {
		width: 12px;
		height: 2px;
		border-radius: 1px;
		background: var(--c);
	}
	.key.area {
		width: 10px;
		height: 10px;
		border-radius: 2px;
	}
	.name {
		color: var(--fg-2);
	}
	.val {
		font-family: var(--font-mono);
		font-variant-numeric: tabular-nums;
		color: var(--fg);
	}
	.plot {
		position: relative;
		min-height: 180px;
	}
	svg {
		display: block;
		width: 100%;
		height: auto;
		overflow: visible;
		touch-action: pan-y;
		border-radius: var(--radius-sm);
	}
	svg:focus-visible {
		outline: none;
		box-shadow: var(--focus-ring);
	}
	.grid line {
		stroke: var(--chart-grid);
		stroke-width: 1;
		shape-rendering: crispEdges;
	}
	.grid line.base {
		stroke: var(--chart-axis);
	}
	.grid text,
	.ref text {
		font-family: var(--font-mono);
		font-size: 10px;
		fill: var(--fg-quiet);
		font-variant-numeric: tabular-nums;
	}
	.line {
		fill: none;
		stroke: var(--c);
		stroke-width: 2;
		stroke-linejoin: round;
		stroke-linecap: round;
	}
	.band {
		fill: var(--c);
		stroke: none;
	}
	.gap {
		fill: none;
		stroke: var(--surface);
		stroke-width: 2;
		stroke-linejoin: round;
	}
	.ref line {
		stroke: var(--fg-3);
		stroke-width: 1;
		shape-rendering: crispEdges;
	}
	.cross {
		stroke: var(--fg-3);
		stroke-width: 1;
		shape-rendering: crispEdges;
	}
	.dot {
		fill: var(--c);
		stroke: var(--surface);
		stroke-width: 2;
	}
	.tip {
		position: absolute;
		top: 0;
		transform: translateX(12px);
		pointer-events: none;
		min-width: 140px;
		padding: var(--space-2) var(--space-3);
		background: var(--surface-raised);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-md);
		box-shadow: var(--shadow);
		font-size: var(--text-sm);
		z-index: 2;
	}
	.tip.flip {
		transform: translateX(calc(-100% - 12px));
	}
	.tip p {
		margin: 0;
	}
	.when {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		margin-bottom: 2px !important;
	}
	.row {
		display: flex;
		align-items: center;
		gap: 6px;
		white-space: nowrap;
	}
	.row strong {
		font-family: var(--font-mono);
		font-variant-numeric: tabular-nums;
		font-weight: 600;
		color: var(--fg);
	}
	.row span:not(.tkey) {
		color: var(--fg-2);
	}
	.row.total {
		border-top: 1px solid var(--border-soft);
		margin-top: 2px !important;
		padding-top: 2px;
		padding-left: 16px;
	}
	.tkey {
		width: 10px;
		height: 2px;
		background: var(--c);
		flex: none;
	}
	.empty {
		display: grid;
		place-content: center;
		margin: 0;
		color: var(--fg-quiet);
		font-size: var(--text-sm);
		border: 1px solid var(--border-soft);
		border-radius: var(--radius-sm);
		text-align: center;
		padding: var(--space-4);
	}
	.empty.err {
		color: var(--danger-text);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}
	.table {
		margin-top: var(--space-2);
		font-size: var(--text-sm);
	}
	.table summary {
		cursor: pointer;
		color: var(--fg-quiet);
		font-size: var(--text-xs);
		font-family: var(--font-mono);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		width: max-content;
	}
	.table summary:hover {
		color: var(--fg-2);
	}
	.table .table-wrap {
		margin-top: var(--space-2);
	}
	.table td,
	.table th {
		padding: var(--space-1) var(--space-3);
	}
</style>
