<script lang="ts">
	let { points, label }: { points: [number, number][]; label: string } = $props();

	const W = 120;
	const H = 32;

	let path = $derived.by(() => {
		if (points.length < 2) return '';
		const xs = points.map((p) => p[0]);
		const ys = points.map((p) => p[1]);
		const x0 = Math.min(...xs);
		const x1 = Math.max(...xs);
		const y1 = Math.max(...ys, 1e-9);
		const sx = (x: number) => ((x - x0) / Math.max(x1 - x0, 1)) * W;
		const sy = (y: number) => H - 2 - (y / y1) * (H - 4);
		return points.map((p, i) => `${i ? 'L' : 'M'}${sx(p[0]).toFixed(1)},${sy(p[1]).toFixed(1)}`).join(' ');
	});
</script>

{#if path}
	<svg
		viewBox="0 0 {W} {H}"
		width={W}
		height={H}
		role="img"
		aria-label="{label}, last hour"
		preserveAspectRatio="none"
	>
		<path d="{path} L{W},{H} L0,{H} Z" class="area" />
		<path d={path} class="line" />
	</svg>
{/if}

<style>
	svg {
		display: block;
		width: 100%;
		height: 32px;
		overflow: visible;
	}
	.line {
		fill: none;
		stroke: var(--accent);
		stroke-width: 1.5;
		vector-effect: non-scaling-stroke;
	}
	.area {
		fill: var(--accent-dim);
		stroke: none;
	}
</style>
