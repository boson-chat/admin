<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '$lib/api';
	import { errorText } from '$lib/toasts.svelte';
	import { datetime, eventName, eventTone } from '$lib/format';
	import type { AuditEntry, AuditPage } from '$lib/types';
	import type { DropdownOption } from '$lib/dropdown';
	import Dropdown from '$lib/components/Dropdown.svelte';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';

	const PAGE = 50;
	let entries = $state<AuditEntry[] | null>(null);
	let events = $state<string[]>([]);
	let cursor = $state('');
	let serverFiltered = $state(true);
	let requires = $state('');
	let error = $state('');
	let moreError = $state('');
	let loading = $state(false);
	let loadingMore = $state(false);

	let params = $derived(page.url.searchParams);
	const q0 = page.url.searchParams;
	let event = $state(q0.get('event') ?? '');
	let actor = $state(q0.get('actor') ?? q0.get('user_id') ?? '');
	let target = $state(q0.get('target') ?? '');
	let since = $state(q0.get('since') ?? '');
	let until = $state(q0.get('until') ?? '');

	const ranges: DropdownOption[] = [
		{ value: '1h', label: 'Last hour' },
		{ value: '24h', label: 'Last 24 hours' },
		{ value: '168h', label: 'Last 7 days' },
		{ value: '720h', label: 'Last 30 days' },
		{ value: '', label: 'Any time' }
	];
	const untils: DropdownOption[] = [
		{ value: '', label: 'Now' },
		{ value: '1h', label: 'An hour ago' },
		{ value: '24h', label: 'A day ago' },
		{ value: '168h', label: 'A week ago' },
		{ value: '720h', label: '30 days ago' }
	];
	let eventOptions = $derived<DropdownOption[]>([
		{ value: '', label: 'All events', group: 'Groups' },
		{
			value: 'admin_*,settings_updated,sso_provider_*,role_*,user_roles_changed',
			label: 'Admin actions',
			description: 'Accounts, roles, invites, settings, sign-in providers',
			group: 'Groups'
		},
		{
			value: 'role_*,user_roles_changed,admin_role_changed',
			label: 'Roles and role changes',
			group: 'Groups'
		},
		{ value: 'login,login_failed,sso_login_failed', label: 'Sign-ins and failures', group: 'Groups' },
		{ value: 'sso_identity_*,sso_login_failed', label: 'SSO links and failures', group: 'Groups' },
		{ value: 'password_*,admin_password_reset_sent', label: 'Password changes and resets', group: 'Groups' },
		...events.map((ev) => ({ value: ev, label: eventName(ev), description: ev, group: 'Single event' })),
		// keep a filter from the URL selectable before the event list loads
		...(event && !event.includes('*') && !event.includes(',') && !events.includes(event)
			? [{ value: event, label: eventName(event), description: event, group: 'Single event' }]
			: [])
	]);

	function query(extra: Record<string, string> = {}) {
		const q = new URLSearchParams();
		for (const k of ['event', 'actor', 'target', 'since', 'until']) {
			const v = params.get(k) ?? (k === 'actor' ? params.get('user_id') : null);
			if (v) q.set(k, v);
		}
		q.set('limit', String(PAGE));
		for (const [k, v] of Object.entries(extra)) q.set(k, v);
		return q;
	}

	$effect(() => {
		const q = query();
		loading = true;
		moreError = '';
		api<AuditPage>('GET', '/audit?' + q)
			.then((r) => {
				entries = r.entries;
				events = r.events;
				cursor = r.next_cursor ?? '';
				serverFiltered = r.server_filtered;
				requires = r.requires ?? '';
				error = '';
			})
			.catch((e) => (error = errorText(e)))
			.finally(() => (loading = false));
	});

	async function more() {
		if (!cursor || loadingMore || !entries) return;
		loadingMore = true;
		moreError = '';
		try {
			const r = await api<AuditPage>('GET', '/audit?' + query({ cursor }));
			const seen = new Set(entries.map((e) => e.id));
			entries = [...entries, ...r.entries.filter((e) => !seen.has(e.id))];
			cursor = r.next_cursor ?? '';
		} catch (e) {
			moreError = errorText(e);
		} finally {
			loadingMore = false;
		}
	}

	// Infinite scroll: load the next page when the end comes into view.
	let sentinel = $state<HTMLElement>();
	$effect(() => {
		if (!sentinel) return;
		const io = new IntersectionObserver(
			(xs) => {
				if (xs.some((x) => x.isIntersecting) && !moreError) more();
			},
			{ rootMargin: '400px' }
		);
		io.observe(sentinel);
		return () => io.disconnect();
	});

	function apply(e?: Event) {
		e?.preventDefault();
		const q = new URLSearchParams();
		if (event) q.set('event', event);
		if (actor.trim()) q.set('actor', actor.trim());
		if (target.trim()) q.set('target', target.trim());
		if (since) q.set('since', since);
		if (until) q.set('until', until);
		const s = q.toString();
		goto('/audit' + (s ? '?' + s : ''), { keepFocus: true, noScroll: true });
	}
	function reset() {
		event = actor = target = since = until = '';
		apply();
	}
	function by(person: string, kind: 'actor' | 'target') {
		if (kind === 'actor') actor = person;
		else target = person;
		apply();
	}

	function details(e: AuditEntry): string {
		if (!e.meta) return '';
		return Object.entries(e.meta)
			.filter(([k]) => k !== 'user_id')
			.map(([k, v]) => `${k.replaceAll('_', ' ')}: ${typeof v === 'object' ? JSON.stringify(v) : v}`)
			.join(', ');
	}
</script>

<svelte:head><title>Audit log · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Audit log</h1>
		<p>Sign-ins, account changes and admin actions recorded by the auth service, newest first.</p>
	</div>
</div>

<form class="filters" onsubmit={apply}>
	<div class="field wide">
		<span id="f-event">Event</span>
		<Dropdown options={eventOptions} bind:value={event} labelledby="f-event" onchange={() => apply()} />
	</div>
	<label class="field">
		<span>Done by</span>
		<input
			type="text"
			bind:value={actor}
			placeholder="Username or usr_…"
			spellcheck="false"
			autocomplete="off"
		/>
	</label>
	<label class="field">
		<span>Affected account</span>
		<input
			type="text"
			bind:value={target}
			placeholder="Username or usr_…"
			spellcheck="false"
			autocomplete="off"
		/>
	</label>
	<div class="field">
		<span id="f-when">From</span>
		<Dropdown options={ranges} bind:value={since} labelledby="f-when" onchange={() => apply()} />
	</div>
	<div class="field">
		<span id="f-until">Until</span>
		<Dropdown options={untils} bind:value={until} labelledby="f-until" onchange={() => apply()} />
	</div>
	<div class="btns">
		<button class="btn">Apply</button>
		{#if params.size}<button class="btn btn-ghost" type="button" onclick={reset}>Clear filters</button>{/if}
	</div>
</form>
{#if !serverFiltered}
	<p class="muted note">
		This auth service filters on its side only from {requires ? `auth ≥ ${requires}` : 'a newer release'};
		until then filters apply to the newest 200 entries and older history can’t be paged.
	</p>
{/if}

<div class="results">
	{#if error}
		<State kind="error">
			{error}
			{#snippet action()}<button class="btn btn-small" onclick={() => apply()}>Try again</button>{/snippet}
		</State>
	{:else if entries === null}
		<State kind="loading" title="Loading the audit log…" />
	{:else if entries.length === 0}
		<State kind="empty" title="Nothing matches these filters">
			Try a longer time range or another event.
			{#snippet action()}{#if params.size}<button class="btn btn-small" onclick={reset}>Clear filters</button
					>{/if}{/snippet}
		</State>
	{:else}
		<div class="table-wrap" aria-busy={loading}>
			<table>
				<thead>
					<tr>
						<th>When</th>
						<th>Event</th>
						<th>By</th>
						<th class="hide-s">Affected</th>
						<th class="hide-m">IP address</th>
						<th class="hide-m">Details</th>
					</tr>
				</thead>
				<tbody>
					{#each entries as e (e.id)}
						{@const who = e.actor_username || e.username}
						<tr>
							<td class="when"
								><Time at={e.at} />
								<div class="muted small hide-m">{datetime(e.at)}</div></td
							>
							<td><span class="ev" data-tone={eventTone(e.event)}>{eventName(e.event)}</span></td>
							<td>
								{#if e.user_id}
									<a href="/users/{e.user_id}">{who || e.user_id}</a>
									<button
										class="pin"
										type="button"
										title="Only show what {who || 'they'} did"
										onclick={() => by(who || e.user_id || '', 'actor')}
										><span class="sr-only">Filter by {who || e.user_id}</span>⌕</button
									>
								{:else}<span class="muted">System</span>{/if}
							</td>
							<td class="hide-s">
								{#if e.target_user_id && e.target_user_id !== e.user_id}
									<a href="/users/{e.target_user_id}">{e.target_username || e.target_user_id}</a>
								{/if}
							</td>
							<td class="hide-m mono">{e.ip}</td>
							<td class="hide-m details">{details(e)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<div class="more" bind:this={sentinel}>
			{#if moreError}
				<span class="err">{moreError}</span>
				<button class="btn btn-small" onclick={more}>Try again</button>
			{:else if cursor}
				<button class="btn btn-small" onclick={more} disabled={loadingMore}
					>{loadingMore ? 'Loading…' : 'Load more'}</button
				>
			{:else}
				<span class="muted">
					{entries.length}
					{entries.length === 1 ? 'entry' : 'entries'}{serverFiltered
						? ', the end of the log for these filters'
						: ''}.
				</span>
			{/if}
		</div>
	{/if}
</div>

<style>
	.filters {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		align-items: end;
		padding: var(--space-4) var(--space-5);
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}
	.filters > .field {
		flex: 1 1 150px;
		max-width: 220px;
	}
	.filters > .field.wide {
		flex-basis: 220px;
		max-width: 300px;
	}
	.btns {
		display: flex;
		gap: var(--space-2);
	}
	.btns .btn {
		min-height: 38px;
	}
	.note {
		font-size: var(--text-xs);
		font-family: var(--font-mono);
		line-height: 1.6;
		margin-top: var(--space-3);
		max-width: 80ch;
	}
	.small {
		font-size: var(--text-xs);
	}
	.when {
		white-space: nowrap;
	}
	.when .small {
		font-family: var(--font-mono);
	}
	.ev {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
	}
	.ev::before {
		content: '';
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--fg-3);
		flex: none;
	}
	.ev[data-tone='bad'] {
		color: var(--danger-text);
	}
	.ev[data-tone='bad']::before {
		background: var(--danger);
	}
	.ev[data-tone='admin']::before {
		background: var(--accent);
	}
	.details {
		color: var(--fg-2);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		max-width: 32ch;
		overflow-wrap: anywhere;
	}
	.results {
		margin-top: var(--space-5);
	}
	.more {
		display: flex;
		gap: var(--space-3);
		align-items: center;
		justify-content: center;
		padding: var(--space-5) 0;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}
	.err {
		color: var(--danger-text);
	}
	.pin {
		margin-left: var(--space-1);
		padding: 0 4px;
		border: 0;
		background: none;
		color: var(--fg-3);
		cursor: pointer;
		font-size: var(--text-sm);
		opacity: 0;
		transition: opacity var(--motion-fast) var(--ease-standard);
	}
	tr:hover .pin,
	.pin:focus-visible {
		opacity: 1;
	}
	.pin:hover {
		color: var(--accent-text);
	}
	@media (max-width: 1000px) {
		.hide-m {
			display: none;
		}
	}
	@media (max-width: 640px) {
		.filters {
			padding: var(--space-4);
		}
		.filters > .field,
		.filters > .field.wide {
			flex-basis: 100%;
			max-width: none;
		}
		.hide-s {
			display: none;
		}
	}
</style>
