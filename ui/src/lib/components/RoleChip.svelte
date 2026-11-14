<script lang="ts">
	import { hexOk } from '$lib/perms';
	import type { RoleRef } from '$lib/types';

	/**
	 * A role as a chip: its colour as a dot and a hairline, its name in mono.
	 * With onremove it gets a remove button (labelled for screen readers).
	 */
	let {
		role,
		size = 'md',
		href,
		onremove,
		removeTitle
	}: {
		role: Pick<RoleRef, 'id' | 'name' | 'color'>;
		size?: 'md' | 'small';
		href?: string;
		onremove?: () => void;
		/** Shown on a disabled remove button: why it can't be removed. */
		removeTitle?: string;
	} = $props();

	let color = $derived(hexOk(role.color) ? role.color : null);
</script>

<span class="chip" class:small={size === 'small'} class:plain={!color} style:--role={color}>
	<span class="dot" aria-hidden="true"></span>
	{#if href}<a {href}>{role.name}</a>{:else}<span class="name">{role.name}</span>{/if}
	{#if onremove || removeTitle}
		<button
			type="button"
			class="x"
			aria-label="Remove {role.name}"
			title={removeTitle ?? `Remove ${role.name}`}
			disabled={!onremove}
			onclick={onremove}>×</button
		>
	{/if}
</span>

<style>
	.chip {
		--role: var(--fg-3);
		display: inline-flex;
		align-items: center;
		gap: 6px;
		max-width: 100%;
		min-height: 24px;
		padding: 2px 8px;
		border-radius: 999px;
		border: 1px solid color-mix(in oklab, var(--role), transparent 55%);
		background: color-mix(in oklab, var(--role), transparent 90%);
		color: var(--fg);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		line-height: 1.3;
		white-space: nowrap;
		vertical-align: middle;
	}
	.plain {
		border-color: var(--border);
		background: var(--surface-hover);
	}
	.small {
		min-height: 20px;
		padding: 1px 7px;
		font-size: 10px;
		gap: 5px;
	}
	.dot {
		flex: none;
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--role);
	}
	.small .dot {
		width: 6px;
		height: 6px;
	}
	.name,
	a {
		overflow: hidden;
		text-overflow: ellipsis;
		min-width: 0;
	}
	a {
		color: inherit;
		text-decoration: none;
	}
	a:hover {
		text-decoration: underline;
		color: inherit;
	}
	.x {
		flex: none;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 16px;
		height: 16px;
		margin-right: -4px;
		padding: 0;
		border: 0;
		border-radius: 50%;
		background: none;
		color: var(--fg-quiet);
		font-size: 13px;
		line-height: 1;
		cursor: pointer;
	}
	.x:hover:not(:disabled) {
		color: var(--fg);
		background: color-mix(in oklab, var(--role), transparent 70%);
	}
	.x:disabled {
		opacity: 0.35;
		cursor: not-allowed;
	}
	@media (forced-colors: active) {
		.dot {
			forced-color-adjust: none;
		}
	}
</style>
