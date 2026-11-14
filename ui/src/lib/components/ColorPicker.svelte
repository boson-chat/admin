<!--
	ColorPicker: a role colour. Swatches for the usual hues, "No colour", and
	a custom mixer (saturation/brightness pad, hue strip, hex field), all
	drawn by the panel so nothing depends on the browser's colour input.
	Pad and strip are sliders: arrow keys move them (Shift for bigger steps).
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { hexToHsv, hsvToHex, normalizeHex, presets } from '$lib/color';

	let {
		value = $bindable(null),
		disabled = false,
		labelledby
	}: { value?: string | null; disabled?: boolean; labelledby?: string } = $props();

	const uid = $props.id();
	let mixing = $state(false);
	let hsv = $state({ h: 30, s: 0.95, v: 0.85 });
	let hexText = $state('');

	// Follow the value (a swatch, reset, or the hex field) into the mixer.
	$effect(() => {
		const v = value;
		const parsed = v ? hexToHsv(v) : null;
		untrack(() => {
			if (parsed && hsvToHex(hsv.h, hsv.s, hsv.v) !== v) {
				// keep the hue when the colour is grey (hue is undefined there)
				hsv = { h: parsed.s === 0 ? hsv.h : parsed.h, s: parsed.s, v: parsed.v };
			}
		});
		hexText = v ?? '';
	});

	let custom = $derived(!!value && !presets.some((p) => p.hex === value));

	function set(next: string | null) {
		if (disabled) return;
		value = next;
	}
	function fromHsv() {
		set(hsvToHex(hsv.h, hsv.s, hsv.v));
	}

	function drag(e: PointerEvent, el: HTMLElement, apply: (x: number, y: number) => void) {
		if (disabled) return;
		el.setPointerCapture(e.pointerId);
		const at = (ev: PointerEvent) => {
			const r = el.getBoundingClientRect();
			apply(
				Math.min(1, Math.max(0, (ev.clientX - r.left) / r.width)),
				Math.min(1, Math.max(0, (ev.clientY - r.top) / r.height))
			);
			fromHsv();
		};
		at(e);
		const up = () => {
			el.removeEventListener('pointermove', at);
			el.removeEventListener('pointerup', up);
			el.removeEventListener('pointercancel', up);
		};
		el.addEventListener('pointermove', at);
		el.addEventListener('pointerup', up);
		el.addEventListener('pointercancel', up);
	}

	function padKey(e: KeyboardEvent) {
		const d = e.shiftKey ? 0.1 : 0.02;
		const moves: Record<string, [number, number]> = {
			ArrowLeft: [-d, 0],
			ArrowRight: [d, 0],
			ArrowUp: [0, d],
			ArrowDown: [0, -d]
		};
		const m = moves[e.key];
		if (!m || disabled) return;
		e.preventDefault();
		hsv.s = Math.min(1, Math.max(0, hsv.s + m[0]));
		hsv.v = Math.min(1, Math.max(0, hsv.v + m[1]));
		fromHsv();
	}
	function hueKey(e: KeyboardEvent) {
		const d = e.shiftKey ? 30 : 4;
		const k = e.key;
		let h = hsv.h;
		if (k === 'ArrowLeft' || k === 'ArrowDown') h -= d;
		else if (k === 'ArrowRight' || k === 'ArrowUp') h += d;
		else if (k === 'Home') h = 0;
		else if (k === 'End') h = 359;
		else return;
		if (disabled) return;
		e.preventDefault();
		hsv.h = Math.min(359, Math.max(0, h));
		fromHsv();
	}

	// Six digits apply as you type; "#abc" short forms on leaving the field.
	function typed() {
		if (/^#?[0-9a-f]{6}$/i.test(hexText.trim())) set(normalizeHex(hexText));
	}
	function left() {
		const n = normalizeHex(hexText);
		if (n) set(n);
		hexText = n ?? value ?? '';
	}
	let hexBad = $derived(hexText.trim() !== '' && !normalizeHex(hexText));
</script>

<div class="picker" class:disabled role="group" aria-labelledby={labelledby}>
	<div class="row">
		<button
			type="button"
			class="sw none"
			aria-pressed={!value}
			title="No colour"
			aria-label="No colour"
			{disabled}
			onclick={() => set(null)}
		></button>
		{#each presets as p (p.hex)}
			<button
				type="button"
				class="sw"
				style:--c={p.hex}
				aria-pressed={value === p.hex}
				title="{p.name} {p.hex}"
				aria-label={p.name}
				{disabled}
				onclick={() => set(p.hex)}
			></button>
		{/each}
		<button
			type="button"
			class="sw mix"
			class:on={custom}
			style:--c={custom ? value : null}
			aria-expanded={mixing}
			aria-controls="mix-{uid}"
			title="Custom colour"
			aria-label="Custom colour"
			{disabled}
			onclick={() => (mixing = !mixing)}
		>
			<span aria-hidden="true">+</span>
		</button>
	</div>

	<div class="mixer" id="mix-{uid}" hidden={!mixing}>
		<div
			class="pad"
			style:--hue={hsvToHex(hsv.h, 1, 1)}
			role="slider"
			tabindex={disabled ? -1 : 0}
			aria-label="Saturation and brightness"
			aria-valuemin={0}
			aria-valuemax={100}
			aria-valuenow={Math.round(hsv.s * 100)}
			aria-valuetext="saturation {Math.round(hsv.s * 100)}%, brightness {Math.round(hsv.v * 100)}%"
			onpointerdown={(e) =>
				drag(e, e.currentTarget as HTMLElement, (x, y) => {
					hsv.s = x;
					hsv.v = 1 - y;
				})}
			onkeydown={padKey}
		>
			<span class="thumb" style:left="{hsv.s * 100}%" style:top="{(1 - hsv.v) * 100}%"></span>
		</div>
		<div
			class="hue"
			role="slider"
			tabindex={disabled ? -1 : 0}
			aria-label="Hue"
			aria-valuemin={0}
			aria-valuemax={359}
			aria-valuenow={Math.round(hsv.h)}
			aria-orientation="horizontal"
			onpointerdown={(e) => drag(e, e.currentTarget as HTMLElement, (x) => (hsv.h = Math.min(359, x * 360)))}
			onkeydown={hueKey}
		>
			<span class="thumb" style:left="{(hsv.h / 360) * 100}%"></span>
		</div>
		<label class="hex">
			<span class="sample" style:--c={value} aria-hidden="true"></span>
			<span class="sr-only">Hex colour</span>
			<input
				type="text"
				bind:value={hexText}
				oninput={typed}
				onblur={left}
				placeholder="#d97706"
				maxlength="7"
				spellcheck="false"
				autocomplete="off"
				aria-invalid={hexBad || undefined}
				{disabled}
			/>
		</label>
	</div>
</div>

<style>
	.picker {
		display: grid;
		gap: var(--space-3);
	}
	.row {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
	}
	.sw {
		--c: var(--surface-hover);
		width: 28px;
		height: 28px;
		padding: 0;
		border: 0;
		border-radius: 50%;
		background: var(--c);
		box-shadow: inset 0 0 0 1px color-mix(in oklab, var(--fg), transparent 85%);
		cursor: pointer;
		transition: transform var(--motion-fast) var(--ease-standard);
	}
	.sw:hover:not(:disabled) {
		transform: scale(1.08);
	}
	.sw[aria-pressed='true'],
	.mix.on {
		box-shadow:
			0 0 0 2px var(--bg),
			0 0 0 4px var(--c);
	}
	.sw:focus-visible {
		box-shadow: var(--focus-ring);
	}
	.none {
		background:
			linear-gradient(
				135deg,
				transparent calc(50% - 1px),
				var(--danger) calc(50% - 1px) calc(50% + 1px),
				transparent calc(50% + 1px)
			),
			var(--surface);
		box-shadow: inset 0 0 0 1px var(--border-strong);
	}
	.none[aria-pressed='true'] {
		box-shadow:
			0 0 0 2px var(--bg),
			0 0 0 4px var(--fg-3);
	}
	.mix {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		background: conic-gradient(from 90deg, #ef4444, #eab308, #10b981, #0ea5e9, #8b5cf6, #ef4444);
		color: #fff;
		font-family: var(--font-mono);
		font-weight: 700;
		font-size: 15px;
	}
	.mix.on {
		background: var(--c);
	}
	.mix span {
		text-shadow: 0 0 3px rgba(0, 0, 0, 0.6);
	}
	.mixer {
		display: grid;
		gap: var(--space-3);
		max-width: 320px;
		padding: var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.mixer[hidden] {
		display: none;
	}
	.pad {
		position: relative;
		height: 140px;
		border-radius: var(--radius-sm);
		background: linear-gradient(to top, #000, transparent), linear-gradient(to right, #fff, var(--hue));
		cursor: crosshair;
		touch-action: none;
	}
	.hue {
		position: relative;
		height: 14px;
		border-radius: 999px;
		background: linear-gradient(to right, #f00, #ff0, #0f0, #0ff, #00f, #f0f, #f00);
		cursor: ew-resize;
		touch-action: none;
	}
	.thumb {
		position: absolute;
		width: 14px;
		height: 14px;
		margin: -7px 0 0 -7px;
		border-radius: 50%;
		border: 2px solid #fff;
		box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.5);
		pointer-events: none;
	}
	.hue .thumb {
		top: 50%;
	}
	.pad:focus-visible,
	.hue:focus-visible {
		box-shadow: var(--focus-ring);
	}
	.hex {
		display: flex;
		align-items: center;
		gap: var(--space-2);
	}
	.sample {
		--c: transparent;
		flex: none;
		width: 38px;
		height: 38px;
		border-radius: var(--radius-sm);
		background: var(--c);
		box-shadow: inset 0 0 0 1px var(--border);
	}
	.hex input {
		max-width: 140px;
		text-transform: lowercase;
	}
	.hex input[aria-invalid='true'] {
		border-color: var(--danger);
	}
	.disabled .sw,
	.disabled .pad,
	.disabled .hue {
		cursor: not-allowed;
		opacity: 0.5;
	}
	.disabled .sw:hover {
		transform: none;
	}
	@media (forced-colors: active) {
		.sw,
		.pad,
		.hue,
		.sample {
			forced-color-adjust: none;
		}
	}
</style>
