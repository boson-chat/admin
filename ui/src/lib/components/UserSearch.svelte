<!--
	UserSearch: an editable combobox (list autocomplete) over /api/users.
	Typing searches by username or email; ↓ ↑ move, Enter picks, Escape
	clears the list. Picked users are handed to onpick; the field empties.
-->
<script lang="ts">
	import { api } from '$lib/api';
	import { errorText } from '$lib/toasts.svelte';
	import type { User } from '$lib/types';

	let {
		onpick,
		exclude = [],
		label = 'Find a user',
		disabled = false,
		placeholder = 'Search by username or email'
	}: {
		onpick: (u: User) => void | Promise<void>;
		/** User IDs that can't be picked (already members), shown as such. */
		exclude?: string[];
		label?: string;
		disabled?: boolean;
		placeholder?: string;
	} = $props();

	const uid = $props.id();
	let q = $state('');
	let results = $state<User[]>([]);
	let open = $state(false);
	let active = $state(-1);
	let loading = $state(false);
	let error = $state('');
	let seq = 0;
	let timer: ReturnType<typeof setTimeout>;

	function search() {
		clearTimeout(timer);
		const term = q.trim();
		if (!term) {
			results = [];
			open = false;
			return;
		}
		timer = setTimeout(async () => {
			const n = ++seq;
			loading = true;
			try {
				const r = await api<{ users: User[] }>(
					'GET',
					'/users?' + new URLSearchParams({ q: term, limit: '8' })
				);
				if (n !== seq) return;
				results = r.users;
				error = '';
				open = true;
				active = results.findIndex((u) => !exclude.includes(u.id));
			} catch (e) {
				if (n === seq) {
					error = errorText(e);
					open = true;
				}
			} finally {
				if (n === seq) loading = false;
			}
		}, 200);
	}

	async function pick(i: number) {
		const u = results[i];
		if (!u || exclude.includes(u.id)) return;
		open = false;
		q = '';
		results = [];
		await onpick(u);
	}

	function move(dir: 1 | -1) {
		if (!results.length) return;
		let i = active;
		for (let k = 0; k < results.length; k++) {
			i = (i + dir + results.length) % results.length;
			if (!exclude.includes(results[i].id)) break;
		}
		active = i;
		document.getElementById(`${uid}-o${i}`)?.scrollIntoView({ block: 'nearest' });
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'ArrowDown') {
			e.preventDefault();
			if (!open && results.length) open = true;
			else move(1);
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			move(-1);
		} else if (e.key === 'Enter') {
			if (open && active >= 0) {
				e.preventDefault();
				pick(active);
			}
		} else if (e.key === 'Escape') {
			if (open) {
				e.preventDefault();
				e.stopPropagation();
				open = false;
			} else q = '';
		}
	}
</script>

<div class="us">
	<label class="sr-only" for="{uid}-in">{label}</label>
	<input
		id="{uid}-in"
		type="search"
		role="combobox"
		aria-autocomplete="list"
		aria-expanded={open}
		aria-controls="{uid}-list"
		aria-activedescendant={open && active >= 0 ? `${uid}-o${active}` : undefined}
		autocomplete="off"
		spellcheck="false"
		{placeholder}
		{disabled}
		bind:value={q}
		oninput={search}
		{onkeydown}
		onblur={() => setTimeout(() => (open = false), 120)}
	/>
	{#if loading}<span class="spin" aria-hidden="true"></span>{/if}
	<ul id="{uid}-list" role="listbox" aria-label={label} hidden={!open}>
		{#if error}
			<li class="msg bad" role="presentation">{error}</li>
		{:else if !results.length}
			<li class="msg" role="presentation">No one matches “{q.trim()}”.</li>
		{/if}
		{#each results as u, i (u.id)}
			{@const taken = exclude.includes(u.id)}
			<!-- keys are handled on the input (aria-activedescendant) -->
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<li
				id="{uid}-o{i}"
				role="option"
				aria-selected={i === active}
				aria-disabled={taken || undefined}
				class:active={i === active}
				onmousedown={(e) => e.preventDefault()}
				onclick={() => pick(i)}
				onpointermove={() => !taken && (active = i)}
			>
				<span class="u">{u.username}</span>
				{#if u.email}<span class="e">{u.email}</span>{/if}
				{#if taken}<span class="t">Already a member</span>{/if}
			</li>
		{/each}
	</ul>
</div>

<style>
	.us {
		position: relative;
	}
	.spin {
		position: absolute;
		right: 12px;
		top: 12px;
		width: 12px;
		height: 12px;
		border-radius: 50%;
		border: 2px solid var(--accent);
		border-right-color: transparent;
		animation: spin 0.9s linear infinite;
	}
	ul {
		position: absolute;
		z-index: 40;
		top: calc(100% + 6px);
		left: 0;
		right: 0;
		max-height: 280px;
		overflow-y: auto;
		margin: 0;
		padding: var(--space-1);
		list-style: none;
		background: var(--surface-raised);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-sm);
		box-shadow: var(--shadow);
	}
	ul[hidden] {
		display: none;
	}
	li {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 2px var(--space-3);
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-sm);
		cursor: pointer;
		font-size: var(--text-sm);
	}
	li.active {
		background: var(--surface-hover);
	}
	li[aria-disabled='true'] {
		cursor: default;
		opacity: 0.6;
	}
	.u {
		font-family: var(--font-mono);
		font-weight: 600;
		color: var(--fg);
	}
	.e {
		color: var(--fg-quiet);
		font-size: var(--text-xs);
		overflow-wrap: anywhere;
	}
	.t {
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.msg {
		cursor: default;
		color: var(--fg-quiet);
	}
	.msg.bad {
		color: var(--danger-text);
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>
