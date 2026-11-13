<script lang="ts">
	import type { Snippet } from 'svelte';
	let { tone = 'bad', children }: { tone?: 'bad' | 'warn' | 'info'; children: Snippet } = $props();
</script>

<div class="notice {tone}" role={tone === 'bad' ? 'alert' : 'note'}>
	{@render children()}
</div>

<style>
	.notice {
		padding: var(--space-3) var(--space-4);
		border-radius: var(--radius-s);
		border: 1px solid;
		font-size: var(--t-sm);
	}
	.bad {
		color: var(--bad);
		border-color: color-mix(in srgb, var(--bad) 40%, transparent);
		background: var(--bad-soft);
	}
	.warn {
		color: var(--warn);
		border-color: color-mix(in srgb, var(--warn) 40%, transparent);
		background: var(--warn-soft);
	}
	.info {
		color: var(--text-2);
		border-color: var(--line-strong);
		background: var(--surface-2);
	}
</style>
