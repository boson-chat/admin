<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		title,
		action,
		danger = false,
		onconfirm,
		children,
		trigger
	}: {
		title: string;
		action: string;
		danger?: boolean;
		onconfirm: () => Promise<void> | void;
		children: Snippet;
		trigger: Snippet<[() => void]>;
	} = $props();

	let dialog: HTMLDialogElement;
	let busy = $state(false);

	function open() {
		dialog.showModal();
	}
	async function confirm() {
		busy = true;
		try {
			await onconfirm();
			dialog.close();
		} finally {
			busy = false;
		}
	}
</script>

{@render trigger(open)}

<dialog bind:this={dialog} aria-labelledby="dlg-title">
	<form method="dialog">
		<h2 id="dlg-title">{title}</h2>
		<div class="body">{@render children()}</div>
		<div class="actions">
			<button class="btn btn-ghost" value="cancel" disabled={busy}>Cancel</button>
			<button
				class="btn {danger ? 'btn-danger' : 'btn-primary'}"
				type="button"
				onclick={confirm}
				disabled={busy}>{busy ? 'Working…' : action}</button
			>
		</div>
	</form>
</dialog>

<style>
	dialog {
		width: min(440px, calc(100vw - 32px));
		padding: var(--space-5);
		border: 1px solid var(--line-strong);
		border-radius: var(--radius-m);
		background: var(--raised);
		color: var(--text);
		box-shadow: var(--shadow);
	}
	dialog::backdrop {
		background: #05070c99;
	}
	.body {
		margin: var(--space-3) 0 var(--space-5);
		color: var(--text-2);
		font-size: var(--t-sm);
	}
	.actions {
		display: flex;
		justify-content: flex-end;
		gap: var(--space-2);
	}
</style>
