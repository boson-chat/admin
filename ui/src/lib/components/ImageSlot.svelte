<!--
	ImageSlot: one branding image (logo, sign-in background, a pane's
	background). Choosing a file, or dropping one onto the slot, uploads it
	right away (the BFF sniffs, checks and re-encodes it); Remove clears the
	slot. Keyboard and screen reader use goes through the file input.
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
	const accepted = accept.split(',');
	// Some platforms drop files with no MIME type; fall back to the name.
	const extensions = /\.(png|jpe?g|webp|gif|svg)$/i;
	let blocked = $derived(disabled || !!busy);
	// dragenter/dragleave fire for every child; count them so the
	// highlight doesn't flicker as the pointer crosses the thumbnail.
	let depth = $state(0);
	let over = $derived(depth > 0 && !blocked);

	function chosen() {
		const file = input?.files?.[0];
		if (input) input.value = '';
		if (file) uploadFile(file);
	}

	async function uploadFile(file: File) {
		error = '';
		if (file.size > 2 * 1024 * 1024) {
			error = `That file is ${(file.size / 1024 / 1024).toFixed(1)} MB; 2 MB at most.`;
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
		}
	}

	const hasFiles = (e: DragEvent) => !!e.dataTransfer && Array.from(e.dataTransfer.types).includes('Files');

	function dragenter(e: DragEvent) {
		if (!hasFiles(e)) return;
		e.preventDefault();
		depth++;
	}

	function dragover(e: DragEvent) {
		if (!hasFiles(e) || blocked) return;
		e.preventDefault();
		e.dataTransfer!.dropEffect = 'copy';
	}

	function dragleave(e: DragEvent) {
		if (!hasFiles(e)) return;
		depth = Math.max(0, depth - 1);
	}

	function drop(e: DragEvent) {
		if (!hasFiles(e)) return;
		e.preventDefault();
		depth = 0;
		if (blocked) return;
		// More than one file: take the first, ignore the rest.
		const file = e.dataTransfer!.files[0];
		if (!file) return;
		if (file.type ? !accepted.includes(file.type) : !extensions.test(file.name)) {
			error = 'That’s not a PNG, JPEG, WebP, GIF or SVG image.';
			return;
		}
		uploadFile(file);
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

<div
	class="slot"
	class:wide
	class:cover
	class:over
	role="group"
	aria-labelledby="{uid}-l"
	ondragenter={dragenter}
	ondragover={dragover}
	ondragleave={dragleave}
	ondrop={drop}
>
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
			{#if over}
				<span class="drop now">Drop to upload</span>
			{:else if !blocked}
				<span class="drop">or drop an image here</span>
			{/if}
		</div>
	</div>
</div>

<style>
	.slot {
		position: relative;
		isolation: isolate;
		display: grid;
		grid-template-columns: 56px minmax(0, 1fr);
		gap: var(--space-3);
		align-items: start;
	}
	/* The drag-over outline sits just outside the slot, so the layout doesn't shift. */
	.slot.over::before {
		content: '';
		position: absolute;
		inset: calc(-1 * var(--space-2));
		z-index: -1;
		border: 1px dashed var(--accent);
		border-radius: var(--radius-md);
		background: var(--accent-dim);
		pointer-events: none;
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
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		margin-top: var(--space-1);
	}
	.drop {
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.drop.now {
		color: var(--accent-text);
		font-weight: 600;
	}
	label.btn:focus-within {
		box-shadow: var(--focus-ring);
	}
	label.dis {
		opacity: 0.4;
		cursor: not-allowed;
	}
</style>
