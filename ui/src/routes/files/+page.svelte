<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { deletedBy, humanSize, type FilePage, type FileRecord, type FileStatus } from '$lib/files';
	import Dropdown from '$lib/components/Dropdown.svelte';
	import type { DropdownOption } from '$lib/dropdown';
	import Confirm from '$lib/components/Confirm.svelte';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';
	import UserSearch from '$lib/components/UserSearch.svelte';
	import type { User } from '$lib/types';

	let user = $derived(page.url.searchParams.get('user') ?? '');
	let userName = $derived(page.url.searchParams.get('name') ?? '');
	let q = $state(page.url.searchParams.get('q') ?? '');
	let applied = $state(page.url.searchParams.get('q') ?? '');
	let list = $state<FileRecord[] | null>(null);
	let cursor = $state('');
	let error = $state('');
	let unsupported = $state('');
	let off = $state(false);
	let loadingMore = $state(false);
	let reason = $state('');
	const statuses: DropdownOption<FileStatus>[] = [
		{ value: 'live', label: 'Live', description: 'Still downloadable' },
		{ value: 'all', label: 'All', description: 'Including deleted and expired' },
		{ value: 'deleted', label: 'Deleted', description: 'By the uploader or a moderator' },
		{ value: 'expired', label: 'Expired', description: 'Past the retention' },
		{ value: 'pending', label: 'Uploading', description: 'Not finished yet' }
	];
	let status = $state<FileStatus>((page.url.searchParams.get('status') as FileStatus) || 'live');
	let configured = $derived(session.info?.features.files !== false);

	async function fetchPage(after = '') {
		const p = new URLSearchParams();
		if (user) p.set('user', user);
		if (applied.trim()) p.set('q', applied.trim());
		if (status !== 'live') p.set('status', status);
		if (after) p.set('cursor', after);
		return api<FilePage>('GET', `/files${p.size ? '?' + p : ''}`);
	}

	async function load() {
		list = null;
		error = unsupported = '';
		off = false;
		try {
			const r = await fetchPage();
			list = r.files;
			cursor = r.next_cursor ?? '';
		} catch (e) {
			const code = (e as { code?: string }).code;
			if (code === 'files_off') off = true;
			else if (code === 'upstream_unsupported') unsupported = errorText(e);
			else error = errorText(e);
			list = [];
		}
	}

	async function more() {
		if (!cursor) return;
		loadingMore = true;
		try {
			const r = await fetchPage(cursor);
			list = [...(list ?? []), ...r.files];
			cursor = r.next_cursor ?? '';
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			loadingMore = false;
		}
	}

	$effect(() => {
		void user;
		void applied;
		void status;
		load();
	});

	function search(e: Event) {
		e.preventDefault();
		applied = q;
		const u = new URL(page.url);
		if (q.trim()) u.searchParams.set('q', q.trim());
		else u.searchParams.delete('q');
		goto(u, { replaceState: true, keepFocus: true, noScroll: true });
	}

	function pick(u: User) {
		const url = new URL(page.url);
		url.searchParams.set('user', u.id);
		url.searchParams.set('name', u.username);
		goto(url, { keepFocus: true, noScroll: true });
	}
	function clearUser() {
		const url = new URL(page.url);
		url.searchParams.delete('user');
		url.searchParams.delete('name');
		goto(url, { keepFocus: true, noScroll: true });
	}

	async function remove(f: FileRecord) {
		try {
			const p = reason.trim() ? `?reason=${encodeURIComponent(reason.trim())}` : '';
			await api('DELETE', `/files/${encodeURIComponent(f.id)}${p}`);
			list = (list ?? []).map((x) =>
				x.id === f.id
					? {
							...x,
							status: 'deleted',
							deleted_at: new Date().toISOString(),
							deleted_by: `admin:${session.info?.user.username ?? ''}`
						}
					: x
			);
			toasts.push(`Deleted ${f.name}. Its link stops working; it still counts against the uploader’s quota.`);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			throw e;
		} finally {
			reason = '';
		}
	}

	function kind(ct: string) {
		const t = (ct || '').split(';')[0];
		if (t.startsWith('image/')) return 'Image';
		if (t.startsWith('video/')) return 'Video';
		if (t.startsWith('audio/')) return 'Audio';
		if (t === 'application/pdf') return 'PDF';
		if (t.startsWith('text/')) return 'Text';
		return t || 'File';
	}
	const expired = (f: FileRecord) => new Date(f.expires_at).getTime() <= Date.now();
</script>

<svelte:head><title>Uploaded files · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Uploaded files</h1>
		<p>
			What people shared through the files service. Deleting removes the file and its link at once; it still
			counts against the uploader’s quota until the upload leaves the window.
		</p>
	</div>
</div>

{#if !configured}
	<State kind="off" title="The files service isn’t connected">
		Set <code>BOSON_FILES_INTERNAL_URL=http://files:3001</code> and <code>BOSON_FILES_INTERNAL_TOKEN</code> on
		the admin service (<code>ADMIN_FILES_INTERNAL_URL</code> and <code>FILES_INTERNAL_TOKEN</code> in the
		stack’s
		<code>.env</code>), then run <code>docker compose up -d admin</code>.
	</State>
{:else}
	<div class="filters">
		<form class="searchbox" role="search" onsubmit={search}>
			<label class="sr-only" for="f-q">Search file names</label>
			<input id="f-q" type="search" bind:value={q} placeholder="Search file names" autocomplete="off" />
			<button class="btn" type="submit">Search</button>
		</form>
		<div class="status-filter">
			<span class="lbl" id="f-status-l">Show</span>
			<Dropdown options={statuses} bind:value={status} labelledby="f-status-l" />
		</div>
		<div class="who">
			{#if user}
				<span class="chip" data-testid="user-filter">
					Uploaded by <strong>{userName || user}</strong>
					<button class="x" type="button" onclick={clearUser} aria-label="Show everyone’s files">×</button>
				</span>
			{:else}
				<UserSearch label="Filter by uploader" placeholder="Uploader’s username or email" onpick={pick} />
			{/if}
		</div>
	</div>

	{#if off}
		<State kind="off" title="The files service isn’t connected">
			The panel has no <code>BOSON_FILES_INTERNAL_URL</code>.
		</State>
	{:else if unsupported}
		<State kind="unsupported" title="Needs a newer files service">{unsupported}.</State>
	{:else if error}
		<State kind="error" title="The files didn’t load">
			{error}
			{#snippet action()}<button class="btn btn-small" onclick={load}>Try again</button>{/snippet}
		</State>
	{:else if list === null}
		<State kind="loading" title="Loading files…" />
	{:else if list.length === 0}
		<State kind="empty" title={applied || user ? 'No files match' : 'Nothing uploaded right now'}>
			{applied || user
				? 'Try another name, or clear the filters.'
				: 'Files show up here while they’re kept (3 days by default).'}
		</State>
	{:else}
		<div class="table-wrap">
			<table>
				<thead>
					<tr>
						<th>File</th>
						<th class="num">Size</th>
						<th>Uploaded by</th>
						<th class="hide-s">Uploaded</th>
						<th class="hide-s">Expires</th>
						<th><span class="sr-only">Actions</span></th>
					</tr>
				</thead>
				<tbody>
					{#each list as f (f.id)}
						{@const gone = !!f.deleted_at || expired(f)}
						<tr class:dim={gone}>
							<td class="file">
								{#if gone}
									<span class="name">{f.name}</span>
								{:else}
									<a class="name" href={f.url} target="_blank" rel="noreferrer noopener">{f.name}</a>
								{/if}
								<div class="muted small mono">
									{kind(f.content_type)}{#if f.width && f.height}&nbsp;· {f.width}×{f.height}{/if}
									{#if f.deleted_at}<span class="tag tag-bad">Deleted</span
										>{#if deletedBy(f.deleted_by)}&nbsp;by {deletedBy(
												f.deleted_by
											)}{/if}{:else if f.status === 'pending'}<span class="tag">Uploading</span
										>{:else if expired(f)}<span class="tag">Expired</span>{/if}
								</div>
							</td>
							<td class="num">{humanSize(f.size)}</td>
							<td>
								{#if f.user_kind === 'account'}
									<a href="/users/{encodeURIComponent(f.user)}">{f.username || f.user}</a>
								{:else}
									<span class="tag">Guest</span>
									<a
										class="muted small mono"
										href="/files?user={encodeURIComponent(f.user)}&name={encodeURIComponent(
											'guest ' + f.user
										)}"
										title="This guest’s files">{f.user}</a
									>
								{/if}
							</td>
							<td class="hide-s"><Time at={f.created_at} /></td>
							<td class="hide-s"
								>{#if gone}—{:else}<Time at={f.expires_at} />{/if}</td
							>
							<td class="num">
								{#if !f.deleted_at}
									<Confirm title="Delete {f.name}?" action="Delete file" danger onconfirm={() => remove(f)}>
										{#snippet trigger(open)}<button class="btn btn-small" onclick={open}>Delete</button
											>{/snippet}
										<p>
											The file and its link go away now, for everyone. The upload ({humanSize(f.size)}) keeps
											counting against {f.user_kind === 'account'
												? f.username || 'the uploader'
												: 'the guest'}’s quota.
										</p>
										<label class="reason">
											<span>Reason (optional, for the log)</span>
											<input
												type="text"
												bind:value={reason}
												maxlength="500"
												placeholder="Spam, illegal content…"
											/>
										</label>
									</Confirm>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		{#if cursor}
			<div class="more">
				<button class="btn" onclick={more} disabled={loadingMore}
					>{loadingMore ? 'Loading…' : 'Load more'}</button
				>
			</div>
		{/if}
	{/if}
{/if}

<style>
	.filters {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3) var(--space-6);
		align-items: end;
		margin-bottom: var(--space-5);
	}
	.searchbox {
		display: flex;
		gap: var(--space-2);
		flex: 1 1 280px;
		max-width: 480px;
	}
	.searchbox input {
		flex: 1;
		min-width: 0;
	}
	.status-filter {
		display: grid;
		gap: var(--space-1);
	}
	.lbl {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
	}
	.who {
		flex: 1 1 260px;
		max-width: 420px;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		padding: var(--space-1) var(--space-1) var(--space-1) var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
		font-size: var(--text-sm);
	}
	.x {
		border: 0;
		background: none;
		color: var(--fg-quiet);
		font-size: var(--text-base);
		line-height: 1;
		padding: var(--space-1) var(--space-2);
		cursor: pointer;
		border-radius: var(--radius-sm);
	}
	.x:hover {
		color: var(--fg);
		background: var(--surface-hover);
	}
	.file {
		min-width: 0;
		max-width: 28rem;
	}
	.name {
		overflow-wrap: anywhere;
	}
	.small {
		font-size: var(--text-xs);
	}
	.small .tag {
		margin-left: var(--space-2);
	}
	tr.dim td {
		color: var(--fg-quiet);
	}
	.reason {
		display: grid;
		gap: var(--space-1);
		margin-top: var(--space-3);
		font-size: var(--text-sm);
	}
	.reason span {
		color: var(--fg-quiet);
		font-size: var(--text-xs);
	}
	td.num {
		white-space: nowrap;
	}
	@media (max-width: 640px) {
		.hide-s {
			display: none;
		}
		td,
		th {
			padding-left: var(--space-3);
			padding-right: var(--space-3);
		}
	}
	.more {
		display: flex;
		justify-content: center;
		margin-top: var(--space-4);
	}
</style>
