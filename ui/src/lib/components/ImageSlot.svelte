<!--
	ImageSlot: one branding image (logo, sign-in background, a pane's
	background). Choosing a file uploads it right away (the BFF sniffs,
	checks and re-encodes it); Remove clears the slot.
-->
<script lang="ts">
	import { upload, api } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import type { ConfigView } from '$lib/types';

	let {
		slot,
		label,
		hint,
		url,
		disabled = false,
		wide = false,
		cover = false,
		onsaved
	}: {
		slot: string;
		label: string;
		hint?: string;
		/** The stored image, or undefined. */
		url?: string;
		disabled?: boolean;
		/** A wide thumbnail (backgrounds, wordmarks). */
		wide?: boolean;
		/** Fill the thumbnail (photos); logos are shown whole. */
		cover?: boolean;
		onsaved: (v: ConfigView) => void;
	} = $props();

	const uid = $props.id();
	let input = $state<HTMLInputElement>();
	let busy = $state('');
	let error = $state('');
	const accept = 'image/png,image/jpeg,image/webp,image/gif,image/svg+xml';

	async function chosen() {
		const file = input?.files?.[0];
		if (!file) return;
		error = '';
		if (file.size > 2 * 1024 * 1024) {
			error = `That file is ${(file.size / 1024 / 1024).toFixed(1)} MB; 2 MB at most.`;
			input!.value = '';
			return;
		}
		busy = 'Uploading…';
		try {
			const r = await upload<{ config: ConfigView }>(
				'PUT',
				`/branding/images/${encodeURIComponent(slot)}`,
				file
			);
			onsaved(r.config);
			toasts.push(`${label} saved.`);
		} catch (e) {
			error = errorText(e);
		} finally {
			busy = '';
			if (input) input.value = '';
		}
	}

	async function remove() {
		busy = 'Removing…';
		error = '';
		try {
			const r = await api<{ config: ConfigView }>('DELETE', `/branding/images/${encodeURIComponent(slot)}`);
			onsaved(r.config);
			toasts.push(`${label} removed.`);
		} catch (e) {
			error = errorText(e);
		} finally {
			busy = '';
		}
	}
</script>

<div class="slot" class:wide class:cover>
	<div class="thumb" class:empty={!url} aria-hidden="true">
		{#if url}<img src={url} alt="" />{:else}<span>none</span>{/if}
	</div>
	<div class="txt">
		<span class="label" id="{uid}-l">{label}</span>
		{#if hint}<span class="hint">{hint}</span>{/if}
		{#if error}<span class="err" role="alert">{error}</span>{/if}
		<div class="btns">
			<label class="btn btn-small" class:dis={disabled || !!busy}>
				<input
					bind:this={input}
					type="file"
					{accept}
					class="sr-only"
					disabled={disabled || !!busy}
					aria-describedby="{uid}-l"
					onchange={chosen}
				/>
				{busy || (url ? 'Replace' : 'Upload')}
			</label>
			{#if url && !busy}
				<button type="button" class="btn btn-small btn-ghost" {disabled} onclick={remove}>Remove</button>
			{/if}
		</div>
	</div>
</div>

<style>
	.slot {
		display: grid;
		grid-template-columns: 56px minmax(0, 1fr);
		gap: var(--space-3);
		align-items: start;
	}
	.slot.wide {
		grid-template-columns: 96px minmax(0, 1fr);
	}
	.thumb {
		height: 56px;
		border-radius: var(--radius-sm);
		border: 1px solid var(--border);
		background: repeating-conic-gradient(var(--surface-hover) 0 25%, var(--surface) 0 50%) 50% / 12px 12px;
		display: grid;
		place-items: center;
		overflow: hidden;
	}
	.thumb.empty {
		background: var(--surface);
		border-style: dashed;
	}
	.thumb span {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	.thumb img {
		width: 100%;
		height: 100%;
		object-fit: contain;
	}
	.cover .thumb img {
		object-fit: cover;
	}
	.txt {
		display: grid;
		gap: 2px;
		min-width: 0;
	}
	.label {
		font-weight: 600;
		font-size: var(--text-sm);
	}
	.hint {
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.err {
		font-size: var(--text-xs);
		color: var(--danger-text);
	}
	.btns {
		display: flex;
		gap: var(--space-2);
		margin-top: var(--space-1);
	}
	label.btn:focus-within {
		box-shadow: var(--focus-ring);
	}
	label.dis {
		opacity: 0.4;
		cursor: not-allowed;
	}
</style>
