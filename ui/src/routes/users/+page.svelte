<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '$lib/api';
	import { errorText } from '$lib/toasts.svelte';
	import type { User } from '$lib/types';
	import type { DropdownOption } from '$lib/dropdown';
	import Dropdown from '$lib/components/Dropdown.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import Time from '$lib/components/Time.svelte';
	import CreateUser from '$lib/components/CreateUser.svelte';
	import RoleChip from '$lib/components/RoleChip.svelte';
	import { session } from '$lib/session.svelte';

	let creator = $state<CreateUser>();

	const sizes: DropdownOption[] = [
		{ value: '25', label: '25 per page' },
		{ value: '50', label: '50 per page' },
		{ value: '100', label: '100 per page' }
	];
	const DEFAULT_SIZE = '50';
	function sizeParam(v: string | null) {
		return sizes.some((s) => s.value === v) ? (v as string) : DEFAULT_SIZE;
	}

	let users = $state<User[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state('');

	let q = $derived(page.url.searchParams.get('q') ?? '');
	let offset = $derived(Math.max(0, Number(page.url.searchParams.get('offset')) || 0));
	let pageSize = $derived(Number(sizeParam(page.url.searchParams.get('limit'))));
	let input = $state(page.url.searchParams.get('q') ?? '');
	let size = $state(sizeParam(page.url.searchParams.get('limit')));

	$effect(() => {
		const query = q;
		const off = offset;
		loading = true;
		const params = new URLSearchParams({ limit: String(pageSize), offset: String(off) });
		if (query) params.set('q', query);
		api<{ users: User[]; total: number }>('GET', '/users?' + params)
			.then((r) => {
				users = r.users;
				total = r.total;
				error = '';
			})
			.catch((e) => (error = errorText(e)))
			.finally(() => (loading = false));
	});

	let timer: ReturnType<typeof setTimeout>;
	function search() {
		clearTimeout(timer);
		timer = setTimeout(() => setParams(input.trim(), 0), 250);
	}
	function setParams(query: string, off: number) {
		const p = new URLSearchParams();
		if (query) p.set('q', query);
		if (off) p.set('offset', String(off));
		if (size !== DEFAULT_SIZE) p.set('limit', size);
		const s = p.toString();
		goto('/users' + (s ? '?' + s : ''), { keepFocus: true, noScroll: true, replaceState: true });
	}
</script>

<svelte:head><title>Users · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Users</h1>
		<p>Everyone with a boson account. Their username is also their IRC nick.</p>
	</div>
	{#if session.can('users.manage')}
		<button class="btn btn-primary" onclick={() => creator?.open()}>Create user</button>
	{/if}
</div>
{#if session.can('users.manage')}<CreateUser bind:this={creator} />{/if}

<div class="toolbar">
	<label class="search">
		<span class="sr-only">Search users</span>
		<input
			type="search"
			placeholder="Search by username or email"
			bind:value={input}
			oninput={search}
			autocomplete="off"
			spellcheck="false"
		/>
	</label>
	<span class="muted count" aria-live="polite">
		{#if !loading || users.length}
			{#if total === 0}No users{:else}{offset + 1}–{offset + users.length} of {total}{/if}
		{/if}
	</span>
	<Dropdown
		class="size"
		options={sizes}
		bind:value={size}
		label="Users per page"
		size="small"
		align="end"
		onchange={() => setParams(q, 0)}
	/>
</div>

{#if error}
	<Notice>{error}</Notice>
{:else}
	<div class="table-wrap" aria-busy={loading}>
		<table>
			<thead>
				<tr>
					<th>User</th>
					<th class="hide-s">Email</th>
					<th>{session.info?.roles_supported ? 'Roles' : 'Role'}</th>
					<th>Status</th>
					<th class="hide-s">Joined</th>
				</tr>
			</thead>
			<tbody>
				{#each users as u (u.id)}
					<tr class:disabled={u.disabled_at}>
						<td>
							<a class="name" href="/users/{u.id}">{u.username}</a>
							{#if u.display_name && u.display_name !== u.username}
								<div class="muted small">{u.display_name}</div>
							{/if}
						</td>
						<td class="hide-s">
							{#if u.email}
								{u.email}
								{#if !u.email_verified}<span class="tag tag-warn">Unverified</span>{/if}
							{:else}<span class="muted">None</span>{/if}
						</td>
						<td class="roles">
							{#if u.roles}
								{#each u.roles.slice(0, 3) as r (r.id)}<RoleChip role={r} size="small" />{/each}
								{#if u.roles.length > 3}<span
										class="muted more"
										title={u.roles
											.slice(3)
											.map((r) => r.name)
											.join(', ')}>+{u.roles.length - 3}</span
									>{/if}
								{#if !u.roles.length}<span class="muted">Member</span>{/if}
							{:else if u.role === 'admin'}<span class="tag tag-accent">Admin</span>{:else}<span class="muted"
									>User</span
								>{/if}
						</td>
						<td
							>{#if u.disabled_at}<span class="tag tag-bad">Disabled</span>{:else}<span class="ok"
									>Active</span
								>{/if}</td
						>
						<td class="hide-s"><Time at={u.created_at} /></td>
					</tr>
				{:else}
					{#if !loading}
						<tr>
							<td colspan="5" class="empty">
								{#if q}No one matches “{q}”. Try part of a username or an email domain.
								{:else}No accounts yet. Create an invite so people can register.{/if}
							</td>
						</tr>
					{/if}
				{/each}
			</tbody>
		</table>
	</div>
	{#if total > pageSize}
		<div class="pager">
			<button
				class="btn btn-small"
				disabled={offset === 0}
				onclick={() => setParams(q, Math.max(0, offset - pageSize))}>Previous</button
			>
			<button
				class="btn btn-small"
				disabled={offset + pageSize >= total}
				onclick={() => setParams(q, offset + pageSize)}>Next</button
			>
		</div>
	{/if}
{/if}

<style>
	.toolbar {
		display: flex;
		align-items: center;
		gap: var(--space-4);
		margin-bottom: var(--space-4);
		flex-wrap: wrap;
	}
	.search {
		flex: 1 1 260px;
		max-width: 420px;
	}
	.toolbar :global(.size) {
		margin-left: auto;
	}
	.count {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-variant-numeric: tabular-nums;
	}
	.name {
		font-family: var(--font-mono);
		font-weight: 600;
		color: var(--fg);
		text-decoration: none;
	}
	.name:hover {
		color: var(--accent-text);
		text-decoration: underline;
	}
	tr.disabled .name {
		color: var(--fg-quiet);
	}
	.small {
		font-size: var(--text-xs);
	}
	.roles {
		min-width: 120px;
	}
	.roles :global(.chip) {
		margin: 1px 4px 1px 0;
	}
	.more {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}
	.ok {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		color: var(--fg-2);
	}
	.ok::before {
		content: '';
		width: 6px;
		height: 6px;
		border-radius: 999px;
		background: var(--success);
	}
	.empty {
		color: var(--fg-quiet);
		padding: var(--space-8) var(--space-4);
		text-align: center;
	}
	.pager {
		display: flex;
		gap: var(--space-2);
		justify-content: flex-end;
		margin-top: var(--space-4);
	}
	@media (max-width: 640px) {
		.hide-s {
			display: none;
		}
	}
</style>
