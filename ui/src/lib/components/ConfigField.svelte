<!--
	One key of the stack config file: its control, where the value comes
	from (the file, or the env default), and Reset to default.

	`value` is the draft. undefined = the key isn't in the file and no
	default is known: the service uses its own; "Set a value" starts one.
-->
<script lang="ts">
	import type { ConfigKey, ConfigValue, VoiceRoom } from '$lib/types';
	import { keyLabels } from '$lib/stackconfig.svelte';
	import Dropdown from './Dropdown.svelte';
	import Toggle from './Toggle.svelte';
	import type { DropdownOption } from '$lib/dropdown';

	let {
		k,
		value = $bindable(),
		onreset,
		resetting = false,
		error = ''
	}: {
		k: ConfigKey;
		value?: ConfigValue;
		onreset?: () => void;
		resetting?: boolean;
		error?: string;
	} = $props();

	const uid = $props.id();
	let meta = $derived(keyLabels[k.key] ?? { label: k.key });
	let disabled = $derived(!k.editable);

	function starter(): ConfigValue {
		switch (k.kind) {
			case 'bool':
				return false;
			case 'int':
				return k.min ?? 0;
			case 'enum':
				return k.enum?.[0] ?? '';
			case 'duration':
				return '10m';
			case 'list':
				return [];
			case 'rooms':
				return [{ id: 'general', name: 'General' }];
			default:
				return '';
		}
	}

	// Lists are edited as one entry per line.
	let listText = $state('');
	let lastList: string[] | undefined;
	$effect(() => {
		if (k.kind !== 'list') return;
		const v = (value as string[] | undefined) ?? [];
		if (v !== lastList) {
			const cur = listText
				.split('\n')
				.map((s) => s.trim())
				.filter(Boolean);
			if (cur.join('\n') !== v.join('\n')) listText = v.join('\n');
			lastList = v;
		}
	});
	function onList() {
		const v = listText
			.split(/[\n,]/)
			.map((s) => s.trim())
			.filter(Boolean);
		lastList = v;
		value = v;
	}

	let rooms = $derived((value as VoiceRoom[] | undefined) ?? []);
	function setRoom(i: number, field: 'id' | 'name', v: string) {
		value = rooms.map((r, j) => (j === i ? { ...r, [field]: v } : r));
	}
	/** A room's voice bitrate cap: '' = the server-wide voice.max_kbps. */
	function setRoomCap(i: number, v: string) {
		value = rooms.map((r, j) => {
			if (j !== i) return r;
			const rest: VoiceRoom = { id: r.id, name: r.name };
			return v ? { ...rest, max_kbps: Number(v) } : rest;
		});
	}
	function capOptions(cur?: number): DropdownOption[] {
		const opts = [
			{ value: '', label: 'No room cap' },
			...roomCaps.map((n) => ({ value: String(n), label: `${n} kbps` }))
		];
		if (cur && !roomCaps.includes(cur)) opts.push({ value: String(cur), label: `${cur} kbps` });
		return opts;
	}
	function addRoom() {
		value = [...rooms, { id: '', name: '' }];
	}
	function removeRoom(i: number) {
		value = rooms.filter((_, j) => j !== i);
	}

	const roomCaps = [24, 32, 48, 64, 96, 128];

	let enumOptions = $derived<DropdownOption[]>((k.enum ?? []).map((v) => ({ value: v, label: v })));
	const durations: DropdownOption[] = ['5m', '10m', '30m', '1h', '6h', '24h'].map((v) => ({
		value: v,
		label: v
	}));

	function show(v: ConfigValue | undefined): string {
		if (v === undefined || v === null) return '';
		if (Array.isArray(v)) {
			if (!v.length) return 'none';
			return v
				.map((x) => (typeof x === 'string' ? x : x.max_kbps ? `${x.id} (${x.max_kbps} kbps)` : `${x.id}`))
				.join(', ');
		}
		return String(v);
	}
</script>

<div class="cfield" class:set={k.set} class:invalid={!!error}>
	<div class="head">
		<div class="txt">
			<span class="label" id="lbl-{uid}">{meta.label}</span>
			{#if meta.detail}<span class="detail" id="det-{uid}">{meta.detail}</span>{/if}
		</div>
		<div class="src">
			{#if k.set}
				<span class="tag tag-accent" title="{k.key} in the config file"
					>In the file{k.line ? ` · line ${k.line}` : ''}</span
				>
				{#if k.editable && onreset}
					<button class="btn btn-small btn-ghost" type="button" onclick={onreset} disabled={resetting}
						>{resetting ? 'Resetting…' : 'Reset to default'}</button
					>
				{/if}
			{:else}
				<span class="tag">Default</span>
			{/if}
		</div>
	</div>

	<div class="ctl">
		{#if value === undefined && k.set}
			<p class="unset">
				The value in the file doesn’t work, so the service keeps its last good one.
				{#if k.editable}
					<button class="btn btn-small" type="button" onclick={() => (value = starter())}
						>Set a new value</button
					>
				{/if}
			</p>
		{:else if value === undefined}
			<p class="unset">
				Not in the file, so the service uses {#if k.env}<code>{k.env}</code> from its environment or{/if} its built-in
				default.
				{#if k.editable}
					<button class="btn btn-small" type="button" onclick={() => (value = starter())}>Set a value</button>
				{/if}
			</p>
		{:else if k.kind === 'bool'}
			<Toggle label={value ? 'On' : 'Off'} bind:checked={value as boolean} {disabled} />
		{:else if k.kind === 'enum'}
			<Dropdown
				options={enumOptions}
				bind:value={value as string}
				labelledby="lbl-{uid}"
				describedby={meta.detail ? `det-${uid}` : undefined}
				{disabled}
			/>
		{:else if k.kind === 'duration'}
			<Dropdown options={durations} bind:value={value as string} labelledby="lbl-{uid}" {disabled} />
		{:else if k.kind === 'int'}
			<input
				type="number"
				inputmode="numeric"
				min={k.min}
				max={k.max}
				step="1"
				aria-labelledby="lbl-{uid}"
				aria-invalid={!!error}
				bind:value
				{disabled}
			/>
		{:else if k.kind === 'string'}
			<input
				type="text"
				aria-labelledby="lbl-{uid}"
				aria-invalid={!!error}
				spellcheck="false"
				autocomplete="off"
				bind:value
				{disabled}
			/>
		{:else if k.kind === 'list'}
			<textarea
				rows={Math.min(8, Math.max(3, listText.split('\n').length + 1))}
				aria-labelledby="lbl-{uid}"
				aria-describedby={meta.detail ? `det-${uid}` : undefined}
				aria-invalid={!!error}
				spellcheck="false"
				bind:value={listText}
				oninput={onList}
				{disabled}></textarea>
		{:else if k.kind === 'rooms'}
			<ul class="rooms" aria-labelledby="lbl-{uid}">
				{#each rooms as r, i (i)}
					<li>
						<label>
							<span class="sr-only">Room {i + 1} id</span>
							<input
								type="text"
								value={r.id}
								placeholder="general"
								spellcheck="false"
								oninput={(e) => setRoom(i, 'id', e.currentTarget.value)}
								{disabled}
							/>
						</label>
						<label>
							<span class="sr-only">Room {i + 1} name</span>
							<input
								type="text"
								value={r.name}
								placeholder="General"
								oninput={(e) => setRoom(i, 'name', e.currentTarget.value)}
								{disabled}
							/>
						</label>
						<Dropdown
							options={capOptions(r.max_kbps)}
							value={r.max_kbps ? String(r.max_kbps) : ''}
							onchange={(v) => setRoomCap(i, v)}
							label="Room {i + 1} voice quality cap"
							title="The most a person sends in this room"
							{disabled}
						/>
						<button
							class="btn btn-small btn-ghost"
							type="button"
							onclick={() => removeRoom(i)}
							aria-label="Remove room {r.name || i + 1}"
							{disabled}>Remove</button
						>
					</li>
				{/each}
			</ul>
			<button class="btn btn-small add" type="button" onclick={addRoom} {disabled}>Add a room</button>
		{/if}
		{#if error}<p class="err" role="alert">{error}</p>{/if}
		{#if !k.set && k.default !== undefined}
			<p class="foot">
				From {k.env ? `${k.env} in the environment` : 'the environment'}{show(k.default)
					? `: ${show(k.default)}`
					: ' (empty)'}.
			</p>
		{/if}
		{#if !k.editable}
			<p class="foot">Changing it needs <code>{k.perm}</code>.</p>
		{/if}
	</div>
</div>

<style>
	.cfield {
		display: grid;
		gap: var(--space-3);
		padding: var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}
	.cfield:last-child {
		border-bottom: 0;
	}
	.head {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: var(--space-2) var(--space-4);
		align-items: start;
	}
	.txt {
		display: grid;
		gap: 2px;
	}
	.label {
		font-family: var(--font-mono);
		font-weight: 600;
		font-size: var(--text-sm);
		color: var(--fg);
	}
	.detail {
		font-size: var(--text-sm);
		color: var(--fg-2);
		line-height: 1.5;
		max-width: 64ch;
	}
	.src {
		display: flex;
		gap: var(--space-2);
		align-items: center;
		flex-wrap: wrap;
		justify-content: flex-end;
	}
	.ctl {
		display: grid;
		gap: var(--space-2);
		max-width: 34rem;
	}
	textarea {
		width: 100%;
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-sm);
		border: 1px solid var(--border);
		background: var(--surface);
		color: var(--fg);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		line-height: 1.6;
		resize: vertical;
	}
	textarea:hover {
		border-color: var(--border-strong);
	}
	textarea:focus-visible {
		outline: none;
		border-color: var(--accent);
		box-shadow:
			0 0 0 1px var(--accent),
			0 0 0 4px var(--accent-dim);
	}
	[aria-invalid='true'] {
		border-color: var(--danger);
	}
	.unset {
		font-size: var(--text-sm);
		color: var(--fg-quiet);
		line-height: 1.7;
	}
	.unset button {
		margin-left: var(--space-2);
	}
	.rooms {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: var(--space-2);
	}
	.rooms li {
		display: grid;
		grid-template-columns: 9rem minmax(0, 1fr) 9.5rem auto;
		gap: var(--space-2);
		align-items: center;
	}
	.rooms label {
		display: block;
	}
	.add {
		justify-self: start;
	}
	.err {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--danger-text);
	}
	.foot {
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		line-height: 1.5;
	}
	@media (max-width: 640px) {
		.head {
			grid-template-columns: minmax(0, 1fr);
		}
		.src {
			justify-content: flex-start;
		}
		.rooms li {
			grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
			padding-bottom: var(--space-2);
			border-bottom: 1px dashed var(--border-soft);
		}
		.rooms li > button {
			justify-self: end;
		}
	}
</style>
