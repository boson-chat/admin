<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '$lib/api';
	import { errorText } from '$lib/toasts.svelte';
	import { datetime, eventName, eventTone } from '$lib/format';
	import type { AuditEntry } from '$lib/types';
	import type { DropdownOption } from '$lib/dropdown';
	import Dropdown from '$lib/components/Dropdown.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import Time from '$lib/components/Time.svelte';

	let entries = $state<AuditEntry[] | null>(null);
	let events = $state<string[]>([]);
	let error = $state('');
	let loading = $state(false);

	let params = $derived(page.url.searchParams);
	let event = $state(page.url.searchParams.get('event') ?? '');
	let userId = $state(page.url.searchParams.get('user_id') ?? '');
	let since = $state(page.url.searchParams.get('since') ?? '');

	const limits: DropdownOption[] = [
		{ value: '50', label: '50 entries' },
		{ value: '100', label: '100 entries' },
		{ value: '200', label: '200 entries', description: 'The most the auth service returns at once' }
	];
	function limitParam(v: string | null) {
		return limits.some((l) => l.value === v) ? (v as string) : '200';
	}

	let limit = $state(limitParam(page.url.searchParams.get('limit')));

	const ranges: DropdownOption[] = [
		{ value: '1h', label: 'Last hour' },
		{ value: '24h', label: 'Last 24 hours' },
		{ value: '168h', label: 'Last 7 days' },
		{ value: '720h', label: 'Last 30 days' },
		{ value: '', label: 'Any time' }
	];
	let eventOptions = $derived<DropdownOption[]>([
		{ value: '', label: 'All events', group: 'Groups' },
		{
			value: 'admin_*',
			label: 'Admin actions',
			description: 'Role changes, disables, invites',
			group: 'Groups'
		},
		{ value: 'login*', label: 'Sign-ins and failures', group: 'Groups' },
		{ value: 'password_*', label: 'Password changes and resets', group: 'Groups' },
		...events.map((ev) => ({ value: ev, label: eventName(ev), description: ev, group: 'Single event' })),
		// keep a filter from the URL selectable before the event list loads
		...(event && !event.endsWith('*') && !events.includes(event)
			? [{ value: event, label: eventName(event), description: event, group: 'Single event' }]
			: [])
	]);

	$effect(() => {
		const q = new URLSearchParams(params);
		q.set('limit', limitParam(params.get('limit')));
		loading = true;
		api<{ entries: AuditEntry[]; events: string[] }>('GET', '/audit?' + q)
			.then((r) => {
				entries = r.entries;
				events = r.events;
				error = '';
			})
			.catch((e) => (error = errorText(e)))
			.finally(() => (loading = false));
	});

	function apply(e?: Event) {
		e?.preventDefault();
		const q = new URLSearchParams();
		if (event) q.set('event', event);
		if (userId.trim()) q.set('user_id', userId.trim());
		if (since) q.set('since', since);
		if (limit !== '200') q.set('limit', limit);
		const s = q.toString();
		goto('/audit' + (s ? '?' + s : ''), { keepFocus: true, noScroll: true });
	}
	function reset() {
		event = '';
		userId = '';
		since = '';
		limit = '200';
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
	<label class="field wide">
		<span>Done by user ID</span>
		<input type="text" bind:value={userId} placeholder="usr_…" spellcheck="false" autocomplete="off" />
	</label>
	<div class="field">
		<span id="f-when">When</span>
		<Dropdown options={ranges} bind:value={since} labelledby="f-when" onchange={() => apply()} />
	</div>
	<div class="field">
		<span id="f-limit">Show</span>
		<Dropdown options={limits} bind:value={limit} labelledby="f-limit" onchange={() => apply()} />
	</div>
	<div class="btns">
		<button class="btn">Apply</button>
		{#if params.size}<button class="btn btn-ghost" type="button" onclick={reset}>Clear filters</button>{/if}
	</div>
</form>
<p class="muted note">
	Shows up to {limitParam(params.get('limit'))} entries. The user filter matches who performed the action; for admin
	actions the affected account is listed separately.
</p>

{#if error}
	<Notice>{error}</Notice>
{:else if entries === null}
	<p class="muted">Loading…</p>
{:else if entries.length === 0}
	<p class="muted empty">Nothing matches these filters.</p>
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
					<tr>
						<td class="when"
							><Time at={e.at} />
							<div class="muted small hide-m">{datetime(e.at)}</div></td
						>
						<td><span class="ev" data-tone={eventTone(e.event)}>{eventName(e.event)}</span></td>
						<td>
							{#if e.user_id}
								<a href="/users/{e.user_id}">{e.username || e.user_id}</a>
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
{/if}

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
		flex: 1 1 160px;
		max-width: 260px;
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
		margin: var(--space-3) 0 var(--space-5);
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
	.empty {
		padding: var(--space-6) 0;
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
