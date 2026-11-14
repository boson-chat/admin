<!--
	UserRoles: a user's roles (assign and remove, within the viewer's place
	in the hierarchy) and the effective permissions they add up to.
-->
<script lang="ts">
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { roleLock } from '$lib/perms';
	import { refreshSession, rolesState } from '$lib/roles.svelte';
	import type { DropdownOption } from '$lib/dropdown';
	import type { Permission, PermissionList, RoleRef, User } from '$lib/types';
	import Dropdown from './Dropdown.svelte';
	import RoleChip from './RoleChip.svelte';

	let { user, onchange }: { user: User; onchange: () => void } = $props();

	let manage = $derived(session.can('roles.manage'));
	let catalog = $state<Permission[]>([]);
	let saving = $state(false);
	// The dropdown's choice; follows the saved roles and resets to them on failure.
	let picked = $derived((user.roles ?? []).map((r) => r.id));

	$effect(() => {
		if (manage) {
			if (!rolesState.loaded) rolesState.load();
		} else {
			api<PermissionList>('GET', '/permissions')
				.then((p) => (catalog = p.permissions))
				.catch(() => (catalog = []));
		}
	});
	let labels = $derived(manage ? rolesState.permissions : catalog);

	let options = $derived<DropdownOption[]>(
		rolesState.roles
			.filter((r) => r.builtin !== 'member')
			.map((r) => {
				const lock = roleLock(r, rolesState.viewer);
				return {
					value: r.id,
					label: r.name,
					description: lock || (r.irc?.oper ? 'IRC oper' : undefined),
					disabled: !!lock
				};
			})
	);
	const lockOf = (r: RoleRef) => {
		const full = rolesState.byId(r.id);
		return full ? roleLock(full, rolesState.viewer) : 'You can’t change this role.';
	};

	async function save(ids: string[]) {
		saving = true;
		try {
			const r = await api<{ roles: RoleRef[] }>('PUT', `/users/${encodeURIComponent(user.id)}/roles`, {
				role_ids: ids
			});
			toasts.push(
				r.roles.length
					? `${user.username} now has ${r.roles.map((x) => x.name).join(', ')}.`
					: `${user.username} has only Member now.`
			);
			onchange();
			rolesState.load();
			if (user.id === session.info?.user.id) refreshSession();
		} catch (e) {
			picked = (user.roles ?? []).map((r) => r.id);
			toasts.push(errorText(e), 'bad');
		} finally {
			saving = false;
		}
	}

	const groupNames: Record<string, string> = { admin: 'Admin panel', network: 'Network', irc: 'IRC' };
	let effective = $derived.by(() => {
		const have = new Set(user.permissions ?? []);
		const out: { group: string; items: { id: string; label: string }[] }[] = [];
		for (const p of labels) {
			if (!have.has(p.id)) continue;
			let g = out.find((x) => x.group === p.group);
			if (!g) out.push((g = { group: p.group, items: [] }));
			g.items.push({ id: p.id, label: p.label });
			have.delete(p.id);
		}
		if (have.size) out.push({ group: 'other', items: [...have].map((id) => ({ id, label: id })) });
		return out;
	});
</script>

<div class="ur">
	<div class="chips" aria-label="Roles">
		{#each user.roles ?? [] as r (r.id)}
			{@const lock = manage ? lockOf(r) : 'You need the “Manage roles” permission.'}
			<RoleChip
				role={r}
				href={manage ? `/roles/${encodeURIComponent(r.id)}` : undefined}
				onremove={manage && !lock && !saving ? () => save(picked.filter((x) => x !== r.id)) : undefined}
				removeTitle={lock || undefined}
			/>
		{/each}
		<span class="member" title="Everyone has Member">Member</span>
	</div>
	{#if manage}
		<div class="edit">
			<span class="sr-only" id="roles-l-{user.id}">Roles for {user.username}</span>
			<Dropdown
				multiple
				{options}
				bind:values={picked}
				onchangemany={save}
				labelledby="roles-l-{user.id}"
				placeholder="Add roles"
				summary={(c) => (c.length === 1 ? '1 role' : `${c.length} roles`)}
				size="small"
				disabled={saving || !rolesState.loaded}
			/>
			<span class="muted hint">You can assign roles below your own highest role.</span>
		</div>
	{/if}

	{#if user.permissions}
		<div class="eff">
			<h3>Effective permissions <span class="n">{user.permissions.length}</span></h3>
			<p class="muted hint">Everything their roles add up to, Member included.</p>
			{#if !effective.length}
				<p class="muted hint">None.</p>
			{:else}
				<div class="groups">
					{#each effective as g (g.group)}
						<div>
							<h4>{groupNames[g.group] ?? 'Other'}</h4>
							<ul>
								{#each g.items as p (p.id)}
									<li title={p.id}>{p.label}</li>
								{/each}
							</ul>
						</div>
					{/each}
				</div>
			{/if}
		</div>
	{/if}
</div>

<style>
	.ur {
		display: grid;
		gap: var(--space-4);
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
	}
	.member {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		padding: 2px 8px;
		border: 1px dashed var(--border-strong);
		border-radius: 999px;
	}
	.edit {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-4);
	}
	.edit :global(.dd) {
		width: 200px;
	}
	.hint {
		font-size: var(--text-xs);
	}
	.eff {
		display: grid;
		gap: var(--space-2);
		padding-top: var(--space-4);
		border-top: 1px solid var(--border-soft);
	}
	.eff h3 {
		display: flex;
		align-items: center;
		gap: var(--space-2);
	}
	.n {
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: 600;
		padding: 0 6px;
		border-radius: 999px;
		background: var(--surface-hover);
		color: var(--fg-quiet);
	}
	.groups {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
		gap: var(--space-4) var(--space-6);
		margin-top: var(--space-2);
	}
	h4 {
		margin: 0 0 var(--space-2);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--fg-quiet);
	}
	ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 3px;
	}
	li {
		font-size: var(--text-sm);
		color: var(--fg-2);
		padding-left: 14px;
		position: relative;
	}
	li::before {
		content: '';
		position: absolute;
		left: 0;
		top: 0.6em;
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--success);
	}
</style>
