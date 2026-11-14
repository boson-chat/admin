<!--
	TokenRow: one theme colour. The swatch opens the panel's colour mixer
	under the row; the hex field takes typing; "No colour" in the mixer (or
	Reset) goes back to the base theme's colour, shown faded.
-->
<script lang="ts">
	import ColorPicker from './ColorPicker.svelte';
	import { normalizeHex } from '$lib/color';

	let {
		label,
		token,
		value = $bindable(null),
		fallback,
		disabled = false
	}: {
		label: string;
		/** The CSS variable name, shown as a hint. */
		token: string;
		value?: string | null;
		/** The base theme's colour, used when value is null. */
		fallback: string;
		disabled?: boolean;
	} = $props();

	const uid = $props.id();
	let open = $state(false);
	// Follows the value; typing overrides it until the value changes.
	let text = $derived(value ?? '');
	function typed() {
		const n = normalizeHex(text);
		if (n && /^#?[0-9a-f]{6}$/i.test(text.trim())) value = n;
	}
	function left() {
		const n = normalizeHex(text);
		if (text.trim() === '') value = null;
		else if (n) value = n;
		text = value ?? '';
	}
	let shown = $derived(value ?? fallback);
	let bad = $derived(text.trim() !== '' && !normalizeHex(text));
</script>

<div class="row" class:set={!!value}>
	<button
		type="button"
		class="sw"
		style:--c={shown}
		aria-expanded={open}
		aria-controls="{uid}-mix"
		aria-label="{label}: {shown}{value ? '' : ' (base)'}. Change"
		{disabled}
		onclick={() => (open = !open)}
	></button>
	<label class="name" for="{uid}-hex">
		<span>{label}</span>
		<code>--{token}</code>
	</label>
	<input
		id="{uid}-hex"
		type="text"
		bind:value={text}
		oninput={typed}
		onblur={left}
		placeholder={fallback.toLowerCase()}
		maxlength="7"
		spellcheck="false"
		autocomplete="off"
		aria-invalid={bad || undefined}
		{disabled}
	/>
	<button
		type="button"
		class="btn btn-small btn-ghost reset"
		disabled={disabled || !value}
		aria-label="Use the base colour for {label}"
		onclick={() => (value = null)}>Reset</button
	>
	{#if open}
		<div class="mix" id="{uid}-mix">
			<ColorPicker bind:value {disabled} />
		</div>
	{/if}
</div>

<style>
	.row {
		display: grid;
		grid-template-columns: 28px minmax(0, 1fr) 92px auto;
		gap: var(--space-2) var(--space-3);
		align-items: center;
		padding: 6px 0;
		border-bottom: 1px solid var(--border-soft);
	}
	.row:last-child {
		border-bottom: 0;
	}
	.sw {
		width: 28px;
		height: 28px;
		padding: 0;
		border-radius: var(--radius-sm);
		border: 1px solid var(--border-strong);
		background: var(--c);
		cursor: pointer;
	}
	.row:not(.set) .sw {
		box-shadow: inset 0 0 0 2px var(--surface);
		opacity: 0.85;
	}
	.sw:disabled {
		cursor: not-allowed;
	}
	.name {
		display: grid;
		min-width: 0;
		line-height: 1.25;
	}
	.name span {
		font-size: var(--text-sm);
		font-weight: 500;
	}
	.row:not(.set) .name span {
		color: var(--fg-2);
		font-weight: 400;
	}
	.name code {
		padding: 0;
		background: none;
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	input {
		min-height: 30px !important;
		padding: 4px 8px !important;
		font-size: var(--text-sm) !important;
	}
	input[aria-invalid] {
		border-color: var(--danger);
	}
	.reset {
		visibility: visible;
	}
	.row:not(.set) .reset {
		visibility: hidden;
	}
	.mix {
		grid-column: 1 / -1;
		padding: var(--space-2) 0 var(--space-3);
	}
</style>
