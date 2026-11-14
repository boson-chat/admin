<script lang="ts">
	import { page } from '$app/state';
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { browser, datetime, eventName, eventTone } from '$lib/format';
	import type { AuditEntry, User, UserSession } from '$lib/types';
	import Confirm from '$lib/components/Confirm.svelte';
	import type { DropdownOption } from '$lib/dropdown';
	import Dropdown from '$lib/components/Dropdown.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import Time from '$lib/components/Time.svelte';

	let id = $derived(page.params.id ?? '');
	let user = $state<User | null>(null);
	let sessions = $state<UserSession[] | null>(null);
	let activity = $state<AuditEntry[] | null>(null);
	let error = $state('');

	let self = $derived(session.info?.user.id === id);

	type Role = User['role'];
	const roles: DropdownOption<Role>[] = [
		{ value: 'user', label: 'User', description: 'Chat and voice; no panel access' },
		{ value: 'admin', label: 'Admin', description: 'This panel, the admin API and voice moderation' }
	];
	// The dropdown's value; reset to the saved role if the change is cancelled.
	let roleChoice = $state<Role>('user');
	$effect(() => {
		if (user) roleChoice = user.role;
	});

	async function loadUser() {
		try {
			user = (await api<{ user: User }>('GET', `/users/${encodeURIComponent(id)}`)).user;
			error = '';
		} catch (e) {
			error = errorText(e);
		}
	}
	async function loadSessions() {
		try {
			sessions = (await api<{ sessions: UserSession[] }>('GET', `/users/${encodeURIComponent(id)}/sessions`))
				.sessions;
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
	}
	async function loadActivity() {
		try {
			activity = (
				await api<{ entries: AuditEntry[] }>('GET', `/users/${encodeURIComponent(id)}/audit?limit=15`)
			).entries;
		} catch {
			activity = [];
		}
	}

	$effect(() => {
		void id;
		user = null;
		sessions = null;
		activity = null;
		loadUser();
		loadSessions();
		loadActivity();
	});

	async function setRole(role: 'admin' | 'user') {
		try {
			user = (await api<{ user: User }>('PATCH', `/users/${encodeURIComponent(id)}`, { role })).user;
			toasts.push(
				role === 'admin' ? `${user.username} is now an admin.` : `${user.username} is now a regular user.`
			);
			loadActivity();
		} catch (e) {
			if (user) roleChoice = user.role;
			toasts.push(errorText(e), 'bad');
		}
	}
	async function setDisabled(disabled: boolean) {
		try {
			user = (
				await api<{ user: User }>(
					'POST',
					`/users/${encodeURIComponent(id)}/${disabled ? 'disable' : 'enable'}`
				)
			).user;
			toasts.push(disabled ? `Disabled ${user.username} and signed them out.` : `Enabled ${user.username}.`);
			loadSessions();
			loadActivity();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
	}
	async function revoke(s: UserSession) {
		try {
			await api('DELETE', `/sessions/${encodeURIComponent(s.id)}`);
			toasts.push('Session revoked.');
			loadSessions();
			loadActivity();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
	}
</script>

<svelte:head><title>{user?.username ?? 'User'} · boson admin</title></svelte:head>

<p class="crumb"><a href="/users">Users</a></p>

{#if error}
	<Notice>{error}</Notice>
{:else if !user}
	<p class="muted" aria-busy="true">Loading…</p>
{:else}
	<div class="page-head">
		<div>
			<h1>
				{user.username}
				{#if user.role === 'admin'}<span class="tag tag-accent">Admin</span>{/if}
				{#if user.disabled_at}<span class="tag tag-bad">Disabled</span>{/if}
				{#if self}<span class="tag">You</span>{/if}
			</h1>
			{#if user.display_name && user.display_name !== user.username}<p>{user.display_name}</p>{/if}
		</div>
		<div class="actions">
			{#if user.disabled_at}
				<Confirm title="Enable {user.username}?" action="Enable account" onconfirm={() => setDisabled(false)}>
					{#snippet trigger(open)}<button class="btn btn-small" onclick={open}>Enable account</button
						>{/snippet}
					They can sign in again with their existing password.
				</Confirm>
			{:else}
				<Confirm
					title="Disable {user.username}?"
					action="Disable account"
					danger
					onconfirm={() => setDisabled(true)}
				>
					{#snippet trigger(open)}
						<button class="btn btn-small btn-danger" onclick={open} disabled={self}>Disable account</button>
					{/snippet}
					This signs them out everywhere and blocks new sign-ins, including IRC SASL. Voice tokens they already
					hold stay valid for up to 5 minutes.
				</Confirm>
			{/if}
		</div>
	</div>

	<dl class="facts">
		<div>
			<dt id="role-l">Role</dt>
			<dd>
				<Confirm
					title={roleChoice === 'admin' ? `Make ${user.username} an admin?` : 'Remove admin role?'}
					action={roleChoice === 'admin' ? 'Make admin' : 'Make regular user'}
					onconfirm={() => setRole(roleChoice)}
					oncancel={() => (roleChoice = user?.role ?? 'user')}
				>
					{#snippet trigger(open)}
						<Dropdown
							class="role"
							options={roles}
							bind:value={roleChoice}
							labelledby="role-l"
							size="small"
							disabled={self}
							title={self ? 'You can’t change your own role' : undefined}
							onchange={open}
						/>
					{/snippet}
					{#if roleChoice === 'admin'}
						Admins can manage every account, revoke sessions and create invites, and get moderator rights in
						voice rooms.
					{:else}
						{user.username} will lose access to this panel and the admin API on their next request.
					{/if}
				</Confirm>
			</dd>
		</div>
		<div>
			<dt>Email</dt>
			<dd>
				{user.email ?? 'None'}
				{#if user.email}
					{#if user.email_verified}<span class="ok small">Verified</span>{:else}<span class="tag tag-warn"
							>Unverified</span
						>{/if}
				{/if}
			</dd>
		</div>
		<div>
			<dt>Joined</dt>
			<dd>{datetime(user.created_at)}</dd>
		</div>
		{#if user.disabled_at}
			<div>
				<dt>Disabled</dt>
				<dd>{datetime(user.disabled_at)}</dd>
			</div>
		{/if}
		<div>
			<dt>User ID</dt>
			<dd class="mono">{user.id}</dd>
		</div>
	</dl>

	<section class="section" aria-labelledby="ses-h">
		<div class="section-head">
			<h2 id="ses-h">Active sessions</h2>
			<p>Each device or app signed in to this account.</p>
		</div>
		{#if sessions === null}
			<p class="muted">Loading…</p>
		{:else if sessions.length === 0}
			<p class="muted">No active sessions.</p>
		{:else}
			<div class="table-wrap">
				<table>
					<thead>
						<tr>
							<th>Client</th>
							<th class="hide-s">IP address</th>
							<th>Last active</th>
							<th class="hide-s">Signed in</th>
							<th><span class="sr-only">Actions</span></th>
						</tr>
					</thead>
					<tbody>
						{#each sessions as s (s.id)}
							<tr>
								<td>
									{browser(s.user_agent)}
									{#if s.current}<span class="tag tag-accent">This panel</span>{/if}
									<div class="muted small">{s.auth_method}</div>
								</td>
								<td class="hide-s mono">{s.ip || '—'}</td>
								<td><Time at={s.last_seen_at} /></td>
								<td class="hide-s"><Time at={s.created_at} /></td>
								<td class="num">
									{#if !s.current}
										<Confirm
											title="Revoke this session?"
											action="Revoke session"
											danger
											onconfirm={() => revoke(s)}
										>
											{#snippet trigger(open)}<button class="btn btn-small" onclick={open}>Revoke</button
												>{/snippet}
											{browser(s.user_agent)} will be signed out the next time it refreshes its token (within 15
											minutes).
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

	<section class="section" aria-labelledby="act-h">
		<div class="section-head">
			<h2 id="act-h">Recent activity</h2>
			<p><a href="/audit?user_id={encodeURIComponent(user.id)}">Full audit log for this account</a></p>
		</div>
		{#if activity === null}
			<p class="muted">Loading…</p>
		{:else if activity.length === 0}
			<p class="muted">Nothing recorded yet.</p>
		{:else}
			<ol class="feed">
				{#each activity as e (e.id)}
					<li data-tone={eventTone(e.event)}>
						<span class="what">
							{eventName(e.event)}
							{#if e.target_username && e.target_user_id !== user.id}
								<a href="/users/{e.target_user_id}">{e.target_username}</a>
							{/if}
						</span>
						<span class="muted small"><Time at={e.at} /> {e.ip ? `from ${e.ip}` : ''}</span>
					</li>
				{/each}
			</ol>
		{/if}
	</section>
{/if}

<style>
	.crumb {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		margin-bottom: var(--space-3);
	}
	.crumb a {
		text-decoration: none;
	}
	.crumb a::before {
		content: '← ';
	}
	.crumb a:hover {
		text-decoration: underline;
	}
	h1 {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		flex-wrap: wrap;
		overflow-wrap: anywhere;
	}
	.actions {
		display: flex;
		gap: var(--space-2);
		flex-wrap: wrap;
	}
	/* fact sheet: the client's settings rows (mono caps label over value) */
	.facts {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
		gap: var(--space-4) var(--space-6);
		margin: 0;
		padding: var(--space-5);
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}
	dt {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		color: var(--fg-quiet);
	}
	dd {
		margin: var(--space-1) 0 0;
		display: flex;
		gap: var(--space-2);
		align-items: center;
		flex-wrap: wrap;
		overflow-wrap: anywhere;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
	}
	.small {
		font-size: var(--text-xs);
	}
	dd :global(.role) {
		min-width: 120px;
	}
	.ok {
		color: var(--success-text);
	}
	.feed {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.feed li {
		display: flex;
		justify-content: space-between;
		gap: var(--space-1) var(--space-4);
		flex-wrap: wrap;
		padding: var(--space-3) var(--space-4) var(--space-3) var(--space-8);
		border-bottom: 1px solid var(--border-soft);
		position: relative;
		font-size: var(--text-sm);
	}
	.feed li:last-child {
		border-bottom: 0;
	}
	.feed li::before {
		content: '';
		position: absolute;
		left: var(--space-4);
		top: 50%;
		width: 6px;
		height: 6px;
		margin-top: -3px;
		border-radius: 50%;
		background: var(--fg-3);
	}
	.feed li[data-tone='bad']::before {
		background: var(--danger);
	}
	.feed li[data-tone='admin']::before {
		background: var(--accent);
	}
	.feed .small {
		font-family: var(--font-mono);
	}
	@media (max-width: 640px) {
		.hide-s {
			display: none;
		}
		.feed li::before {
			top: calc(var(--space-3) + 0.75em);
		}
	}
</style>
