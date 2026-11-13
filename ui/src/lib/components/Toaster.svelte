<script lang="ts">
	import { toasts } from '$lib/toasts.svelte';
</script>

<div class="toaster" role="status" aria-live="polite">
	{#each toasts.list as t (t.id)}
		<div class="toast" class:bad={t.tone === 'bad'}>
			<span>{t.text}</span>
			<button class="x" onclick={() => toasts.dismiss(t.id)} aria-label="Dismiss">×</button>
		</div>
	{/each}
</div>

<style>
	.toaster {
		position: fixed;
		right: var(--space-4);
		bottom: var(--space-4);
		display: grid;
		gap: var(--space-2);
		z-index: 50;
		max-width: min(420px, calc(100vw - 32px));
	}
	.toast {
		display: flex;
		align-items: flex-start;
		gap: var(--space-3);
		padding: var(--space-3) var(--space-4);
		background: var(--raised);
		border: 1px solid var(--line-strong);
		border-left: 3px solid var(--ok);
		border-radius: var(--radius-s);
		box-shadow: var(--shadow);
		font-size: var(--t-sm);
		animation: in 160ms ease-out;
	}
	.toast.bad {
		border-left-color: var(--bad);
	}
	.toast span {
		flex: 1;
	}
	.x {
		background: none;
		border: 0;
		color: var(--muted);
		font-size: 18px;
		line-height: 1;
		cursor: pointer;
		padding: 0 2px;
	}
	@keyframes in {
		from {
			transform: translateY(6px);
			opacity: 0;
		}
	}
</style>
