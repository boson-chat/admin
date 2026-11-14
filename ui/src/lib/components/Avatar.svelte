<script lang="ts">
	/** A profile picture, or initials on a tinted square when there is none. */
	let { src, name, size = 56 }: { src?: string; name: string; size?: number } = $props();
	let failed = $state(false);
	let initials = $derived(
		name
			.replace(/[^\p{L}\p{N} ]/gu, ' ')
			.trim()
			.split(/\s+/)
			.slice(0, 2)
			.map((w) => w[0]?.toUpperCase() ?? '')
			.join('') || '?'
	);
</script>

{#if src && !failed}
	<img
		class="av"
		{src}
		alt=""
		width={size}
		height={size}
		style:--size="{size}px"
		onerror={() => (failed = true)}
		referrerpolicy="no-referrer"
	/>
{:else}
	<span class="av initials" style:--size="{size}px" aria-hidden="true">{initials}</span>
{/if}

<style>
	.av {
		width: var(--size);
		height: var(--size);
		flex: none;
		border-radius: var(--radius-md);
		object-fit: cover;
		background: var(--surface-hover);
		box-shadow: inset 0 0 0 1px var(--border);
	}
	.initials {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		font-family: var(--font-display);
		font-weight: 700;
		font-size: calc(var(--size) * 0.36);
		color: var(--accent-text);
		background: color-mix(in oklab, var(--accent), transparent 86%);
		box-shadow: none;
	}
</style>
