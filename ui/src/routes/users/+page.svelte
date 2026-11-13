<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '$lib/api';
	import { errorText } from '$lib/toasts.svelte';
	import type { User } from '$lib/types';
	import Notice from '$lib/components/Notice.svelte';
	import Time from '$lib/components/Time.svelte';

	const PAGE = 50;

	let users = $state<User[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state('');

	let q = $derived(page.url.searchParams.get('q') ?? '');
	let offset = $derived(Math.max(0, Number(page.url.searchParams.get('offset')) || 0));
	let input = $state(page.url.searchParams.get('q') ?? '');

	$effect(() => {
		const query = q;
		const off = offset;
		loading = true;
		const params = new URLSearchParams({ limit: String(PAGE), offset: String(off) });
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
</div>

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
					<th>Role</th>
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
						<td
							>{#if u.role === 'admin'}<span class="tag tag-accent">Admin</span>{:else}<span class="muted"
									>User</span
								>{/if}</td
						>
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
	{#if total > PAGE}
		<div class="pager">
			<button
				class="btn btn-small"
				disabled={offset === 0}
				onclick={() => setParams(q, Math.max(0, offset - PAGE))}>Previous</button
			>
			<button
				class="btn btn-small"
				disabled={offset + PAGE >= total}
				onclick={() => setParams(q, offset + PAGE)}>Next</button
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
	.count {
		font-size: var(--t-sm);
	}
	.name {
		font-weight: 600;
		color: var(--text);
		text-decoration: none;
	}
	.name:hover {
		color: var(--accent);
		text-decoration: underline;
	}
	tr.disabled .name {
		color: var(--muted);
	}
	.small {
		font-size: var(--t-xs);
	}
	.ok {
		color: var(--ok);
	}
	.empty {
		color: var(--muted);
		padding: var(--space-6) 0;
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
