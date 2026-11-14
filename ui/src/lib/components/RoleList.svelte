<!--
	RoleList: the network's roles, most senior first. Drag a role by its
	handle (mouse, pen or touch) to reorder, or focus the handle and press
	↑ / ↓. Owner stays on top and Member at the bottom; roles at or above
	your own can't move.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { hexOk, roleLock } from '$lib/perms';
	import { moveRole, pinned, rolesState, sorted } from '$lib/roles.svelte';
	import type { Role } from '$lib/types';

	let preview = $state<Role[] | null>(null);
	let dragging = $state<string | null>(null);
	let saving = $state(false);
	let announce = $state('');
	let creating = $state(false);
	let listEl: HTMLOListElement;

	let shown = $derived(preview ?? rolesState.roles);
	let current = $derived(page.params.id ?? '');
	const movable = (r: Role) => !pinned(r) && !roleLock(r, rolesState.viewer);
	let canReorder = $derived(rolesState.roles.some(movable));

	async function commit(next: Role[]) {
		const before = rolesState.roles;
		if (next.map((r) => r.id).join() === before.map((r) => r.id).join()) {
			preview = null;
			return;
		}
		rolesState.roles = next; // optimistic
		preview = null;
		saving = true;
		try {
			const r = await api<{ roles: Role[] }>('POST', '/roles/reorder', {
				// auth takes the movable roles only; Owner and Member keep their places
				order: next.filter((r) => !pinned(r)).map((r) => r.id)
			});
			rolesState.roles = sorted(r.roles);
		} catch (e) {
			rolesState.roles = before;
			toasts.push(errorText(e), 'bad');
			announce = 'Reorder failed; the roles are back in their old order.';
		} finally {
			saving = false;
		}
	}

	function startDrag(e: PointerEvent, role: Role) {
		if (!movable(role) || saving || e.button !== 0) return;
		e.preventDefault();
		// Listen on the window: reordering moves the handle in the DOM, which
		// would drop a pointer capture on it.
		dragging = role.id;
		preview = [...rolesState.roles];
		const move = (ev: PointerEvent) => {
			if (!preview) return;
			const items = [...listEl.querySelectorAll<HTMLElement>('li[data-id]')];
			let to = items.length;
			for (let i = 0; i < items.length; i++) {
				const r = items[i].getBoundingClientRect();
				if (ev.clientY < r.top + r.height / 2) {
					to = i;
					break;
				}
			}
			const from = preview.findIndex((r) => r.id === role.id);
			if (to > from) to -= 1; // it leaves its old slot first
			preview = moveRole(preview, from, to, movable);
		};
		const end = (ev: PointerEvent) => {
			window.removeEventListener('pointermove', move);
			window.removeEventListener('pointerup', end);
			window.removeEventListener('pointercancel', end);
			dragging = null;
			if (ev.type === 'pointercancel' || !preview) {
				preview = null;
				return;
			}
			const next = preview;
			const at = next.findIndex((r) => r.id === role.id);
			announce = `${role.name} moved to position ${at + 1} of ${next.length}.`;
			commit(next);
		};
		window.addEventListener('pointermove', move);
		window.addEventListener('pointerup', end);
		window.addEventListener('pointercancel', end);
	}

	let keyTimer: ReturnType<typeof setTimeout>;
	function onHandleKey(e: KeyboardEvent, role: Role) {
		if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return;
		e.preventDefault();
		if (!movable(role) || saving) return;
		const list = preview ?? rolesState.roles;
		const from = list.findIndex((r) => r.id === role.id);
		const next = moveRole(list, from, from + (e.key === 'ArrowUp' ? -1 : 1), movable);
		if (next === list) {
			announce = `${role.name} can’t move further ${e.key === 'ArrowUp' ? 'up' : 'down'}.`;
			return;
		}
		preview = next;
		const at = next.findIndex((r) => r.id === role.id);
		announce = `${role.name}, position ${at + 1} of ${next.length}.`;
		// Save once the keys stop.
		clearTimeout(keyTimer);
		keyTimer = setTimeout(() => preview && commit(preview), 700);
		requestAnimationFrame(() =>
			listEl.querySelector<HTMLElement>(`li[data-id="${CSS.escape(role.id)}"] .handle`)?.focus()
		);
	}

	async function create() {
		creating = true;
		try {
			const r = await api<{ role: Role }>('POST', '/roles', { name: 'new role' });
			rolesState.put(r.role);
			await goto(`/roles/${encodeURIComponent(r.role.id)}?new=1`);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			creating = false;
		}
	}
</script>

<div class="rl">
	<div class="head">
		<span class="count" id="roles-count"
			>{rolesState.roles.length} {rolesState.roles.length === 1 ? 'role' : 'roles'}</span
		>
		<button class="btn btn-small btn-primary" onclick={create} disabled={creating || !rolesState.loaded}
			>{creating ? 'Creating…' : 'Create role'}</button
		>
	</div>
	{#if canReorder}
		<p class="hint" id="reorder-hint">
			Drag ⋮⋮ to reorder, or focus it and press ↑ ↓. Higher roles outrank lower ones.
		</p>
	{/if}
	<ol bind:this={listEl} aria-labelledby="roles-count" class:busy={saving}>
		{#each shown as r, i (r.id)}
			{@const lock = roleLock(r, rolesState.viewer)}
			{@const canMove = movable(r)}
			<li data-id={r.id} class:current={r.id === current} class:dragging={dragging === r.id}>
				{#if canMove && canReorder}
					<button
						type="button"
						class="handle"
						aria-label="Move {r.name}, position {i + 1} of {shown.length}"
						aria-describedby="reorder-hint"
						onpointerdown={(e) => startDrag(e, r)}
						onkeydown={(e) => onHandleKey(e, r)}
					>
						<span aria-hidden="true">⋮⋮</span>
					</button>
				{:else}
					<span class="handle off" aria-hidden="true"
						>{#if r.builtin === 'owner' || r.builtin === 'member'}·{:else}⋮⋮{/if}</span
					>
				{/if}
				<a
					href="/roles/{encodeURIComponent(r.id)}"
					aria-current={r.id === current ? 'page' : undefined}
					style:--role={hexOk(r.color) ? r.color : null}
				>
					<span class="dot" class:none={!hexOk(r.color)} aria-hidden="true"></span>
					<span class="name">{r.name}</span>
					{#if r.builtin}<span class="tag">Built-in</span>{/if}
					{#if lock}<span class="lock" title={lock}><span class="sr-only">Locked: {lock}</span></span>{/if}
					<span class="n" title="{r.member_count} {r.member_count === 1 ? 'member' : 'members'}">
						{#if r.builtin === 'member'}all{:else}{r.member_count}{/if}
						<span class="sr-only">members</span>
					</span>
				</a>
			</li>
		{/each}
	</ol>
	<p class="sr-only" aria-live="assertive">{announce}</p>
</div>

<style>
	.rl {
		display: grid;
		gap: var(--space-3);
		align-content: start;
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
	}
	.count {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.hint {
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		line-height: 1.5;
	}
	ol {
		list-style: none;
		margin: 0;
		padding: var(--space-1);
		display: grid;
		gap: 2px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	ol.busy {
		opacity: 0.7;
	}
	li {
		display: grid;
		grid-template-columns: 24px minmax(0, 1fr);
		align-items: center;
		border-radius: var(--radius-sm);
		transition: background-color var(--motion-fast) var(--ease-standard);
	}
	li.dragging {
		background: var(--surface-raised);
		box-shadow:
			0 0 0 1px var(--accent),
			var(--shadow);
		position: relative;
		z-index: 2;
	}
	.handle {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		height: 36px;
		padding: 0;
		border: 0;
		background: none;
		color: var(--fg-3);
		font-size: 12px;
		letter-spacing: -2px;
		cursor: grab;
		touch-action: none;
		border-radius: var(--radius-sm);
	}
	button.handle:hover {
		color: var(--fg);
		background: var(--surface-hover);
	}
	li.dragging .handle {
		cursor: grabbing;
		color: var(--accent-text);
	}
	.handle.off {
		cursor: default;
		opacity: 0.4;
	}
	a {
		--role: var(--fg-3);
		display: flex;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
		padding: var(--space-2) var(--space-2) var(--space-2) var(--space-1);
		border-radius: var(--radius-sm);
		color: var(--fg-2);
		text-decoration: none;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		font-weight: 600;
	}
	a:hover {
		color: var(--fg);
		background: var(--surface-hover);
	}
	a[aria-current='page'] {
		color: var(--fg);
		background: var(--surface-raised);
		box-shadow: inset 2px 0 0 var(--role);
	}
	.dot {
		flex: none;
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: var(--role);
	}
	.dot.none {
		background: none;
		box-shadow: inset 0 0 0 1.5px var(--fg-3);
	}
	.name {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.lock {
		flex: none;
		width: 9px;
		height: 8px;
		margin-top: 3px;
		border-radius: 1px;
		background: var(--fg-3);
		position: relative;
	}
	.lock::before {
		content: '';
		position: absolute;
		left: 1.5px;
		top: -5px;
		width: 6px;
		height: 6px;
		border: 1.5px solid var(--fg-3);
		border-bottom: 0;
		border-radius: 4px 4px 0 0;
		box-sizing: border-box;
	}
	.n {
		margin-left: auto;
		flex: none;
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-quiet);
		font-variant-numeric: tabular-nums;
	}
	@media (forced-colors: active) {
		.dot {
			forced-color-adjust: none;
		}
	}
</style>
