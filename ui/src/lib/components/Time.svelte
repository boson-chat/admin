<script lang="ts">
	import { ago, datetime, relative } from '$lib/format';
	let {
		at,
		empty = '—',
		compact = false
	}: { at: string | null | undefined; empty?: string; compact?: boolean } = $props();
</script>

{#if at}
	<time datetime={at} title={datetime(at)}>{compact ? ago(at) : relative(at)}</time>
{:else}
	<span class="muted">{empty}</span>
{/if}
