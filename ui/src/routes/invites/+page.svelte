<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { date } from '$lib/format';
	import type { Invite } from '$lib/types';
	import Confirm from '$lib/components/Confirm.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import Time from '$lib/components/Time.svelte';

	let invites = $state<Invite[] | null>(null);
	let registration = $state('');
	let error = $state('');

	let maxUses = $state(1);
	let ttl = $state('168h');
	let note = $state('');
	let creating = $state(false);
	let created = $state<Invite | null>(null);
	let copied = $state(false);

	const expiries = [
		{ v: '1h', label: '1 hour' },
		{ v: '24h', label: '1 day' },
		{ v: '168h', label: '7 days' },
		{ v: '720h', label: '30 days' },
		{ v: '', label: 'Never' }
	];

	async function load() {
		try {
			const r = await api<{ invites: Invite[]; registration: string }>('GET', '/invites');
			invites = r.invites;
			registration = r.registration;
			error = '';
		} catch (e) {
			error = errorText(e);
		}
	}
	onMount(load);

	async function create(e: SubmitEvent) {
		e.preventDefault();
		creating = true;
		try {
			const r = await api<{ invite: Invite }>('POST', '/invites', { max_uses: maxUses, ttl, note });
			created = r.invite;
			copied = false;
			note = '';
			load();
		} catch (err) {
			toasts.push(errorText(err), 'bad');
		} finally {
			creating = false;
		}
	}

	async function copy() {
		if (!created?.code) return;
		try {
			await navigator.clipboard.writeText(created.code);
			copied = true;
		} catch {
			toasts.push('Copy failed. Select the code and copy it by hand.', 'bad');
		}
	}

	async function revoke(inv: Invite) {
		try {
			await api('DELETE', `/invites/${encodeURIComponent(inv.id)}`);
			toasts.push('Invite revoked.');
			if (created?.id === inv.id) created = null;
			load();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
	}

	type State = { word: string; tone: string };
	function inviteState(inv: Invite): State {
		if (inv.revoked_at) return { word: 'Revoked', tone: '' };
		if (inv.expires_at && new Date(inv.expires_at).getTime() < Date.now())
			return { word: 'Expired', tone: '' };
		if (inv.uses >= inv.max_uses) return { word: 'Used up', tone: '' };
		return { word: 'Active', tone: 'tag-ok' };
	}
	function usable(inv: Invite) {
		return inviteState(inv).word === 'Active';
	}
</script>

<svelte:head><title>Invites · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Invites</h1>
		<p>Invite codes let people register when sign-up is invite-only.</p>
	</div>
</div>

{#if registration === 'open'}
	<Notice tone="info">Registration is open, so anyone can sign up without a code. Invites still work.</Notice>
{:else if registration === 'closed'}
	<Notice tone="warn">
		Registration is closed, so invite codes are not accepted either. Set <code>REGISTRATION=invite</code> on the
		auth service to use them.
	</Notice>
{/if}

<div class="layout">
	<form class="create" onsubmit={create}>
		<h2>New invite</h2>
		<label class="field">
			<span>Uses</span>
			<input type="number" min="1" max="10000" bind:value={maxUses} required />
			<small>How many accounts can register with this code.</small>
		</label>
		<label class="field">
			<span>Expires after</span>
			<select bind:value={ttl}>
				{#each expiries as x (x.v)}<option value={x.v}>{x.label}</option>{/each}
			</select>
		</label>
		<label class="field">
			<span>Note</span>
			<input type="text" bind:value={note} maxlength="200" placeholder="Who it’s for" />
			<small>Only admins see this.</small>
		</label>
		<button class="btn btn-primary" disabled={creating}>{creating ? 'Creating…' : 'Create invite'}</button>
	</form>

	{#if created?.code}
		<section class="reveal" aria-labelledby="new-code-h" aria-live="polite">
			<h2 id="new-code-h">Invite created</h2>
			<p>Copy the code now. It is stored hashed and can’t be shown again.</p>
			<div class="code-row">
				<code class="code">{created.code}</code>
				<button class="btn btn-small" onclick={copy}>{copied ? 'Copied' : 'Copy code'}</button>
			</div>
			<p class="muted small">
				{created.max_uses === 1 ? 'Single use' : `${created.max_uses} uses`},
				{created.expires_at ? `expires ${date(created.expires_at)}` : 'never expires'}{created.note
					? `, for ${created.note}`
					: ''}.
			</p>
			<button class="btn btn-small" onclick={() => (created = null)}>Done</button>
		</section>
	{/if}
</div>

<section class="section" aria-labelledby="list-h">
	<div class="section-head"><h2 id="list-h">All invites</h2></div>
	{#if error}
		<Notice>{error}</Notice>
	{:else if invites === null}
		<p class="muted">Loading…</p>
	{:else if invites.length === 0}
		<p class="muted">No invites yet. Create one above to let someone register.</p>
	{:else}
		<div class="table-wrap">
			<table>
				<thead>
					<tr>
						<th>Note</th>
						<th>Status</th>
						<th class="num">Used</th>
						<th class="hide-s">Expires</th>
						<th class="hide-s">Created</th>
						<th><span class="sr-only">Actions</span></th>
					</tr>
				</thead>
				<tbody>
					{#each invites as inv (inv.id)}
						{@const st = inviteState(inv)}
						<tr class:dim={!usable(inv)}>
							<td
								>{inv.note || 'No note'}
								<div class="muted small mono">{inv.id}</div></td
							>
							<td><span class="tag {st.tone}">{st.word}</span></td>
							<td class="num">{inv.uses} of {inv.max_uses}</td>
							<td class="hide-s">{inv.expires_at ? date(inv.expires_at) : 'Never'}</td>
							<td class="hide-s"><Time at={inv.created_at} /></td>
							<td class="num">
								{#if usable(inv)}
									<Confirm
										title="Revoke this invite?"
										action="Revoke invite"
										danger
										onconfirm={() => revoke(inv)}
									>
										{#snippet trigger(open)}<button class="btn btn-small" onclick={open}>Revoke</button
											>{/snippet}
										Nobody can register with it after this. Accounts already created with it stay.
									</Confirm>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</section>

<style>
	.layout {
		display: grid;
		grid-template-columns: minmax(260px, 340px) minmax(0, 1fr);
		gap: var(--space-8);
		align-items: start;
		margin-top: var(--space-6);
	}
	.create {
		display: grid;
		gap: var(--space-5);
		padding: var(--space-6);
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}
	.create .btn {
		justify-self: start;
	}
	/* the one-time reveal: the accent is reserved for this moment */
	.reveal {
		display: grid;
		gap: var(--space-3);
		padding: var(--space-5) var(--space-6);
		border: 1px solid color-mix(in oklab, var(--accent), transparent 55%);
		border-left: 3px solid var(--accent);
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
		background: color-mix(in oklab, var(--accent), transparent 92%);
		animation: reveal var(--motion-medium) var(--ease-out) both;
	}
	.reveal h2 {
		color: var(--accent-text);
	}
	.reveal p {
		color: var(--fg-2);
		font-size: var(--text-sm);
	}
	.reveal > .btn {
		justify-self: start;
	}
	.code-row {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		flex-wrap: wrap;
		margin: var(--space-1) 0;
	}
	.code {
		font-size: var(--text-xl);
		font-weight: 600;
		letter-spacing: 0.04em;
		padding: var(--space-3) var(--space-4);
		background: var(--bg);
		color: var(--fg);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		user-select: all;
		overflow-wrap: anywhere;
	}
	.reveal p.small {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.small {
		font-size: var(--text-xs);
	}
	tr.dim td {
		color: var(--fg-quiet);
	}
	@keyframes reveal {
		from {
			opacity: 0;
			transform: translateY(-4px);
		}
	}
	@media (max-width: 820px) {
		.layout {
			grid-template-columns: 1fr;
		}
	}
	@media (max-width: 640px) {
		.hide-s {
			display: none;
		}
		.create {
			padding: var(--space-5) var(--space-4);
		}
		.reveal {
			padding: var(--space-5) var(--space-4);
		}
		.code {
			font-size: var(--text-base);
			word-break: break-all;
		}
		td .mono {
			word-break: break-all;
		}
	}
</style>
