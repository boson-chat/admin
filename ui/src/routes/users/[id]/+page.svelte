<script lang="ts">
	import { page } from '$app/state';
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { browser, datetime, eventName, eventTone } from '$lib/format';
	import type {
		AuditEntry,
		DirectoryServer,
		OwnedNetworksResponse,
		ProfileResponse,
		User,
		UserSession
	} from '$lib/types';
	import Avatar from '$lib/components/Avatar.svelte';
	import State from '$lib/components/State.svelte';
	import Confirm from '$lib/components/Confirm.svelte';
	import type { DropdownOption } from '$lib/dropdown';
	import Dropdown from '$lib/components/Dropdown.svelte';
	import Time from '$lib/components/Time.svelte';
	import UserRoles from '$lib/components/UserRoles.svelte';
	import { userLock } from '$lib/perms';
	import { rolesState } from '$lib/roles.svelte';
	import { formatSize, humanDuration, humanIn, humanSize, usedShare, type FilesUsage } from '$lib/files';

	let id = $derived(page.params.id ?? '');
	let user = $state<User | null>(null);
	let sessions = $state<UserSession[] | null>(null);
	let activity = $state<AuditEntry[] | null>(null);
	let profile = $state<ProfileResponse | null>(null);
	// Listings they own in the stack's own directory (DIRECTORY_LOCAL_URL);
	// otherwise the boson API's profile read lists them.
	let owned = $state<OwnedNetworksResponse | null>(null);
	let ownedFromDirectory = $derived(!!session.info?.features.directory_local);
	// Uploads: their usage of the rolling quota (files service).
	let usage = $state<{ usage?: FilesUsage; error?: string } | null>(null);
	let filesOn = $derived(!!session.info?.features.files);
	let canModerateFiles = $derived(session.can('users.moderate') || session.can('users.manage'));
	let error = $state('');
	let mailing = $state<'' | 'reset' | 'verification'>('');

	let self = $derived(session.info?.user.id === id);
	let canManage = $derived(session.can('users.manage'));
	// auth: disabling, enabling and revoking sessions take users.moderate or
	// users.manage, and outranking the user.
	let targetLock = $derived(user ? userLock(user.roles, rolesState.viewer, self) : '');
	let canModerate = $derived((session.can('users.moderate') || session.can('users.manage')) && !targetLock);
	$effect(() => {
		if (session.info?.roles_supported && !rolesState.loaded) rolesState.load();
	});
	// auth >= 1.5.0 reports roles; older releases have role=user|admin.
	let hasRoles = $derived(user?.roles !== undefined);

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

	async function loadProfile() {
		try {
			profile = await api<ProfileResponse>('GET', `/users/${encodeURIComponent(id)}/profile`);
		} catch (e) {
			profile = { configured: true, error: errorText(e) };
		}
	}

	async function loadOwned() {
		try {
			owned = await api<OwnedNetworksResponse>('GET', `/users/${encodeURIComponent(id)}/networks`);
		} catch (e) {
			owned = { configured: true, error: errorText(e) };
		}
	}

	let canSetQuota = $derived(session.can('settings.manage'));
	let ovQuota = $state('');
	let ovMax = $state('');
	let savingOv = $state(false);
	async function saveOverride(e: Event) {
		e.preventDefault();
		savingOv = true;
		try {
			const u = await api<FilesUsage>('PUT', `/users/${encodeURIComponent(id)}/files-quota`, {
				quota: ovQuota,
				max_file: ovMax
			});
			usage = { usage: u };
			ovQuota = ovMax = '';
			toasts.push(`Set ${user?.username}’s uploads quota.`);
		} catch (err) {
			toasts.push(errorText(err), 'bad');
		} finally {
			savingOv = false;
		}
	}
	async function clearOverride() {
		savingOv = true;
		try {
			usage = { usage: await api<FilesUsage>('DELETE', `/users/${encodeURIComponent(id)}/files-quota`) };
			toasts.push(`${user?.username} is back on the network’s uploads quota.`);
		} catch (err) {
			toasts.push(errorText(err), 'bad');
		} finally {
			savingOv = false;
		}
	}

	async function loadUsage() {
		try {
			usage = { usage: await api<FilesUsage>('GET', `/users/${encodeURIComponent(id)}/files-usage`) };
		} catch (e) {
			usage = { error: errorText(e) };
		}
	}

	$effect(() => {
		void id;
		usage = null;
		if (filesOn) loadUsage();
		user = null;
		sessions = null;
		activity = null;
		profile = null;
		owned = null;
		loadUser();
		loadSessions();
		loadActivity();
		loadProfile();
		if (ownedFromDirectory) loadOwned();
	});

	async function sendMail(kind: 'reset' | 'verification') {
		mailing = kind;
		try {
			await api(
				'POST',
				`/users/${encodeURIComponent(id)}/${kind === 'reset' ? 'password-reset' : 'verification'}`
			);
			toasts.push(
				kind === 'reset'
					? `Sent ${user?.username} a password reset link.`
					: `Sent ${user?.username} a new verification email.`
			);
			loadActivity();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			mailing = '';
		}
	}

	const statusTag: Record<string, string> = { verified: 'tag-ok', pending: 'tag-warn', lapsed: 'tag-bad' };

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

{#snippet serverList(list: DirectoryServer[])}
	{#if !list.length}
		<p class="muted small">None.</p>
	{:else}
		<ul class="servers">
			{#each list as srv (srv.id)}
				<li>
					<span class="sname">{srv.name}</span>
					<span class="mono muted">{srv.hostname}:{srv.port}{srv.tls ? '' : ' (plain)'}</span>
					<span class="tag {statusTag[srv.verification_status] ?? ''}">{srv.verification_status}</span>
					{#if srv.health_status && srv.health_status !== 'unknown'}
						<span class="health" data-h={srv.health_status}>{srv.health_status}</span>
					{/if}
					{#if srv.user_count !== undefined && srv.user_count !== null}<span class="muted small"
							>{srv.user_count} users</span
						>{/if}
				</li>
			{/each}
		</ul>
	{/if}
{/snippet}

<svelte:head><title>{user?.username ?? 'User'} · boson admin</title></svelte:head>

<p class="crumb"><a href="/users">Users</a></p>

{#if error}
	<State kind="error">
		{error}
		{#snippet action()}<a class="btn btn-small" href="/users">All users</a>{/snippet}
	</State>
{:else if !user}
	<State kind="loading" />
{:else}
	<div class="page-head">
		<div class="who">
			<Avatar
				src={profile?.profile?.avatar_url}
				name={profile?.profile?.display_name || user.display_name || user.username}
				size={56}
			/>
			<div>
				<h1>
					{user.username}
					{#if !hasRoles && user.role === 'admin'}<span class="tag tag-accent">Admin</span>{/if}
					{#if user.disabled_at}<span class="tag tag-bad">Disabled</span>{/if}
					{#if user.has_password === false}
						{#if user.identities?.length}<span class="tag" title="Signs in only through a sign-in provider"
								>SSO only</span
							>{:else}<span class="tag tag-warn" title="No password and no linked sign-in yet"
								>No password yet</span
							>{/if}
					{/if}
					{#if self}<span class="tag">You</span>{/if}
				</h1>
				{#if user.display_name && user.display_name !== user.username}<p>{user.display_name}</p>{/if}
			</div>
		</div>
		<div class="actions">
			{#if user.email && canManage}
				{#if !user.email_verified}
					<button class="btn btn-small" onclick={() => sendMail('verification')} disabled={!!mailing}
						>{mailing === 'verification' ? 'Sending…' : 'Resend verification'}</button
					>
				{/if}
				<Confirm
					title="Send {user.username} a password reset?"
					action="Send reset link"
					onconfirm={() => sendMail('reset')}
				>
					{#snippet trigger(open)}
						<button class="btn btn-small" onclick={open} disabled={!!mailing}
							>{mailing === 'reset' ? 'Sending…' : 'Send password reset'}</button
						>
					{/snippet}
					They get an email at <strong>{user.email}</strong> with a link to choose a new password. Their current
					password keeps working until they do.
				</Confirm>
			{/if}
			{#if !canModerate}
				<!-- disabling and enabling need users.moderate -->
			{:else if user.disabled_at}
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
		{#if !hasRoles}
			<div>
				<dt id="role-l">Role</dt>
				<dd>
					{#if !session.can('roles.manage')}
						{user.role === 'admin' ? 'Admin' : 'User'}
					{:else}
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
								Admins can manage every account, revoke sessions and create invites, and get moderator rights
								in voice rooms.
							{:else}
								{user.username} will lose access to this panel and the admin API on their next request.
							{/if}
						</Confirm>
					{/if}
				</dd>
			</div>
		{/if}
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

	{#if hasRoles}
		<section class="section" aria-labelledby="roles-h">
			<div class="section-head">
				<h2 id="roles-h">Roles</h2>
				<p>Network-wide: they also set {user.username}’s IRC oper class and vhost on their next IRC login.</p>
			</div>
			<UserRoles {user} onchange={loadUser} />
		</section>
	{/if}

	<section class="section" aria-labelledby="prof-h">
		<div class="section-head">
			<h2 id="prof-h">Boson profile</h2>
			<p>What other people see in the boson app.</p>
		</div>
		{#if profile === null}
			<State kind="loading" title="Loading profile…" />
		{:else if !profile.configured}
			<State kind="off" title="Profiles aren’t connected">
				Profiles come from the stack’s boson API. This is a deployment setting: set <code
					>ADMIN_BOSON_API_URL</code
				>
				in the stack’s <code>.env</code> (for example <code>http://api:3000</code>) and run
				<code>docker compose up -d admin</code> to show avatars, handles and the servers people list in the directory.
			</State>
		{:else if profile.unsupported}
			<State kind="unsupported" title="Needs a newer boson API">{profile.unsupported}.</State>
		{:else if profile.error}
			<State kind="error" title="The profile didn’t load">
				{profile.error}
				{#snippet action()}<button class="btn btn-small" onclick={loadProfile}>Try again</button>{/snippet}
			</State>
		{:else if !profile.profile}
			<State kind="empty" title="No boson profile yet">
				{user.username} hasn’t opened the boson app with this account, so there’s no handle or avatar.
			</State>
		{:else}
			{@const p = profile.profile}
			<div class="profile">
				<Avatar src={p.avatar_url} name={p.display_name || p.handle} size={72} />
				<dl class="pfacts">
					<div>
						<dt>Display name</dt>
						<dd>{p.display_name || '—'}</dd>
					</div>
					<div>
						<dt>Handle</dt>
						<dd>@{p.handle}</dd>
					</div>
					<div>
						<dt>Discoverable</dt>
						<dd>{p.is_discoverable ? 'Yes, others can find them' : 'No, hidden from lookups'}</dd>
					</div>
					{#if p.created_at}
						<div>
							<dt>Profile created</dt>
							<dd>{datetime(p.created_at)}</dd>
						</div>
					{/if}
				</dl>
			</div>
			{#if !ownedFromDirectory}
				<h3 class="sub">Servers they list in the directory</h3>
				{@render serverList(profile.servers ?? [])}
			{/if}
		{/if}
	</section>

	{#if filesOn}
		<section class="section" aria-labelledby="up-h">
			<div class="section-head">
				<h2 id="up-h">Uploads</h2>
				<p>
					{#if canModerateFiles}<a
							href="/files?user={encodeURIComponent(user.id)}&name={encodeURIComponent(user.username)}"
							>Their files</a
						>{:else}What they uploaded in the quota window.{/if}
				</p>
			</div>
			{#if usage === null}
				<State kind="loading" title="Asking the files service…" />
			{:else if usage.error}
				<State kind="error" title="Usage didn’t load">
					{usage.error}
					{#snippet action()}<button class="btn btn-small" onclick={loadUsage}>Try again</button>{/snippet}
				</State>
			{:else if usage.usage}
				{@const u = usage.usage}
				{@const share = usedShare(u.used, u.quota)}
				{@const next = u.frees.find((f) => new Date(f.at).getTime() > Date.now())}
				<div class="usage" data-testid="files-usage">
					<p class="usage-line">
						<strong>{humanSize(u.used)}</strong> of {humanSize(u.quota)}
						<span class="muted">per {humanDuration(u.window_seconds)}</span>
					</p>
					<div
						class="meter"
						class:hot={share >= 0.9}
						role="meter"
						aria-label="Quota used"
						aria-valuemin="0"
						aria-valuemax="100"
						aria-valuenow={Math.round(share * 100)}
					>
						<span style:width="{share * 100}%"></span>
					</div>
					<p class="muted small">
						{#if next}{humanSize(next.bytes)} frees in {humanIn(
								new Date(next.at).getTime() - Date.now()
							)}.{:else if u.used > 0}Frees as uploads leave the window.{:else}Nothing uploaded in the window.{/if}
						Largest file {humanSize(u.max_file)}.{#if u.live_files !== undefined}&nbsp;{u.live_files} live
							{u.live_files === 1 ? 'file' : 'files'} ({humanSize(u.live_bytes)}).{/if}
						Deleted files still count until they leave the window.
					</p>
					<div class="override" data-testid="files-override">
						<p class="small">
							{#if u.override && (u.override.quota_bytes !== null || u.override.max_file_bytes !== null)}
								<span class="tag tag-accent">Override</span>
								{u.override.quota_bytes !== null ? humanSize(u.override.quota_bytes) : 'default'} quota,
								{u.override.max_file_bytes !== null ? humanSize(u.override.max_file_bytes) : 'default'} largest
								file{#if u.override.updated_by}&nbsp;· set by {u.override.updated_by.replace(
										/^admin:/,
										''
									)}{/if}
							{:else}
								Network default{#if u.defaults}: {humanSize(u.defaults.quota_bytes)} per window, {humanSize(
										u.defaults.max_file_bytes
									)} per file{/if}.
							{/if}
						</p>
						{#if canSetQuota}
							<form class="ov-form" onsubmit={saveOverride}>
								<label>
									<span>Quota</span>
									<input
										type="text"
										bind:value={ovQuota}
										placeholder={u.defaults ? formatSize(u.defaults.quota_bytes) : '5GB'}
										autocomplete="off"
										spellcheck="false"
									/>
								</label>
								<label>
									<span>Largest file</span>
									<input
										type="text"
										bind:value={ovMax}
										placeholder={u.defaults ? formatSize(u.defaults.max_file_bytes) : '100MB'}
										autocomplete="off"
										spellcheck="false"
									/>
								</label>
								<div class="ov-btns">
									<button class="btn btn-small" disabled={savingOv || (!ovQuota.trim() && !ovMax.trim())}
										>{savingOv ? 'Saving…' : 'Set override'}</button
									>
									{#if u.override}
										<button
											class="btn btn-small btn-ghost"
											type="button"
											onclick={clearOverride}
											disabled={savingOv}>Back to default</button
										>
									{/if}
								</div>
							</form>
						{/if}
					</div>
				</div>
			{/if}
		</section>
	{/if}

	{#if ownedFromDirectory}
		<section class="section" aria-labelledby="own-h">
			<div class="section-head">
				<h2 id="own-h">Directory listings</h2>
				<p>Networks {user.username} registered in this stack’s directory, in any state.</p>
			</div>
			{#if owned === null}
				<State kind="loading" title="Asking the directory…" />
			{:else if owned.unsupported}
				<State kind="unsupported" title="Needs a newer directory">{owned.unsupported}.</State>
			{:else if owned.error}
				<State kind="error" title="The listings didn’t load">
					{owned.error}
					{#snippet action()}<button class="btn btn-small" onclick={loadOwned}>Try again</button>{/snippet}
				</State>
			{:else}
				{@render serverList(owned.networks ?? [])}
			{/if}
		</section>
	{/if}

	<section class="section" aria-labelledby="id-h">
		<div class="section-head">
			<h2 id="id-h">Linked sign-ins</h2>
			<p><a href="/sso">Sign-in providers</a></p>
		</div>
		{#if user.identities === undefined}
			<State kind="unsupported" title="Not reported by this auth service">
				Linked SSO accounts are listed once the auth service is updated (auth ≥ v1.4.0).
			</State>
		{:else if user.identities.length === 0}
			<p class="muted small">
				{#if user.has_password === false}
					None, and no password yet: {user.username} can sign in once they set one from the emailed link or a password
					reset.
				{:else}
					None. {user.username} signs in with a password only.
				{/if}
			</p>
		{:else}
			{#if user.has_password === false}
				<p class="small sso-only">
					SSO only: {user.username} has no password. Removing these sign-ins or their provider locks them out until
					they reset one.
				</p>
			{/if}
			<div class="table-wrap">
				<table>
					<thead>
						<tr>
							<th>Provider</th>
							<th class="hide-s">Account at the provider</th>
							<th>Last used</th>
							<th class="hide-s">Linked</th>
						</tr>
					</thead>
					<tbody>
						{#each user.identities as ident (ident.id)}
							<tr>
								<td>
									<a href="/sso/{encodeURIComponent(ident.provider_id)}"
										>{ident.provider_display_name || ident.provider_slug}</a
									>
									{#if ident.provider_kind}<span class="tag">{ident.provider_kind.toUpperCase()}</span>{/if}
								</td>
								<td class="hide-s mono">
									{ident.email ?? ident.subject}
									{#if ident.email}<div class="muted small">{ident.subject}</div>{/if}
								</td>
								<td><Time at={ident.last_login_at} empty="Never" /></td>
								<td class="hide-s"><Time at={ident.created_at} /></td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>

	<section class="section" aria-labelledby="ses-h">
		<div class="section-head">
			<h2 id="ses-h">Active sessions</h2>
			<p>Each device or app signed in to this account.</p>
		</div>
		{#if sessions === null}
			<State kind="loading" />
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
									{#if !s.current && canModerate}
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
			<p><a href="/audit?actor={encodeURIComponent(user.id)}">Full audit log for this account</a></p>
		</div>
		{#if activity === null}
			<State kind="loading" />
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
	.who {
		display: flex;
		gap: var(--space-4);
		align-items: center;
		min-width: 0;
	}
	.usage {
		display: grid;
		gap: var(--space-2);
		max-width: 36rem;
	}
	.override {
		display: grid;
		gap: var(--space-2);
		margin-top: var(--space-2);
		padding-top: var(--space-3);
		border-top: 1px solid var(--border-soft);
	}
	.ov-form {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-3);
		align-items: end;
	}
	.ov-form label {
		display: grid;
		gap: var(--space-1);
		flex: 1 1 8rem;
		max-width: 11rem;
	}
	.ov-form label span {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
	}
	.ov-btns {
		display: flex;
		gap: var(--space-2);
	}
	.usage-line {
		font-family: var(--font-mono);
		font-variant-numeric: tabular-nums;
	}
	.meter {
		height: 8px;
		border-radius: 4px;
		background: var(--border-soft);
		overflow: hidden;
	}
	.meter span {
		display: block;
		height: 100%;
		background: var(--accent);
	}
	.meter.hot span {
		background: var(--danger);
	}
	.profile {
		display: flex;
		gap: var(--space-5);
		align-items: flex-start;
		padding: var(--space-5);
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}
	.pfacts {
		flex: 1;
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
		gap: var(--space-4) var(--space-6);
		margin: 0;
	}
	.sub {
		margin: var(--space-6) 0 var(--space-3);
		font-size: var(--text-sm);
	}
	.sso-only {
		margin-bottom: var(--space-3);
		color: var(--warn-text);
		font-family: var(--font-mono);
	}
	.servers {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.servers li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-4);
		align-items: center;
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--border-soft);
		font-size: var(--text-sm);
	}
	.servers li:last-child {
		border-bottom: 0;
	}
	.sname {
		font-family: var(--font-mono);
		font-weight: 600;
	}
	.health {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-2);
	}
	.health::before {
		content: '';
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--fg-3);
	}
	.health[data-h='up']::before {
		background: var(--success);
	}
	.health[data-h='down']::before {
		background: var(--danger);
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
		.profile {
			flex-direction: column;
		}
		.hide-s {
			display: none;
		}
		.feed li::before {
			top: calc(var(--space-3) + 0.75em);
		}
	}
</style>
