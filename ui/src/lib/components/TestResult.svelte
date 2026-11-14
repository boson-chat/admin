<script lang="ts">
	import { detailLines } from '$lib/sso';

	/** The outcome of a provider's "Test connection": a verdict and its details. */
	let { ok, details }: { ok: boolean; details: unknown } = $props();
	let lines = $derived(detailLines(details));
	// The failure reason first; the rest is what the test found.
	let error = $derived(
		lines.find(([k]) => k === 'error')?.[1] ?? (lines.length === 1 && !lines[0][0] ? lines[0][1] : '')
	);
	let rest = $derived(lines.filter(([k]) => k !== 'error' && k !== ''));
</script>

<div class="result" class:ok role="status">
	<p>
		<strong>{ok ? 'Connection works.' : 'Test failed.'}</strong>
		{error}
	</p>
	{#if rest.length}
		<dl>
			{#each rest as [k, v] (k)}
				<dt>{k}</dt>
				<dd>{v}</dd>
			{/each}
		</dl>
	{/if}
</div>

<style>
	.result {
		display: grid;
		gap: var(--space-2);
		font-size: var(--text-sm);
		font-family: var(--font-mono);
		padding: var(--space-3) var(--space-4);
		border-left: 3px solid var(--danger);
		background: color-mix(in oklab, var(--danger), transparent 92%);
		color: var(--fg-2);
		overflow-wrap: anywhere;
	}
	strong {
		color: var(--danger-text);
	}
	.ok {
		border-left-color: var(--success);
		background: color-mix(in oklab, var(--success), transparent 92%);
	}
	.ok strong {
		color: var(--success-text);
	}
	dl {
		display: grid;
		grid-template-columns: max-content minmax(0, 1fr);
		gap: 2px var(--space-4);
		margin: 0;
		font-size: var(--text-xs);
	}
	dt {
		color: var(--fg-quiet);
	}
	dd {
		margin: 0;
		color: var(--fg-2);
	}
	@media (max-width: 640px) {
		dl {
			grid-template-columns: minmax(0, 1fr);
		}
		dd {
			margin-bottom: var(--space-1);
		}
	}
</style>
