<script lang="ts">
	import { tick } from 'svelte';
	import { beforeNavigate, goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { defaultWhois, hexOk, nameProblem, permLock, roleLock, vhostFor, vhostProblem } from '$lib/perms';
	import { refreshSession, rolesState } from '$lib/roles.svelte';
	import { EVERYONE } from '$lib/channels';
	import type { Permission, Role, RoleMember, User } from '$lib/types';
	import Time from '$lib/components/Time.svelte';
	import ColorPicker from '$lib/components/ColorPicker.svelte';
	import Confirm from '$lib/components/Confirm.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import RoleChannels from '$lib/components/RoleChannels.svelte';
	import RoleChip from '$lib/components/RoleChip.svelte';
	import State from '$lib/components/State.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import UserSearch from '$lib/components/UserSearch.svelte';
	import WhoisPreview from '$lib/components/WhoisPreview.svelte';

	type Tab = 'display' | 'permissions' | 'channels' | 'irc' | 'members';
	const tabs: { id: Tab; label: string }[] = [
		{ id: 'display', label: 'Display' },
		{ id: 'permissions', label: 'Permissions' },
		{ id: 'channels', label: 'Channels' },
		{ id: 'irc', label: 'IRC' },
		{ id: 'members', label: 'Members' }
	];

	interface Draft {
		name: string;
		color: string | null;
		permissions: string[];
		/** Editable strings: '' is sent as null (default line, no vhost). */
		irc: { whois_line: string; vhost: string; oper: boolean };
		hoist: boolean;
		mentionable: boolean;
	}

	let id = $derived(page.params.id ?? '');
	let role = $derived(rolesState.byId(id));
	let tab = $derived<Tab>(
		tabs.some((t) => t.id === page.url.searchParams.get('tab'))
			? (page.url.searchParams.get('tab') as Tab)
			: 'display'
	);
	let lock = $derived(role ? roleLock(role, rolesState.viewer) : '');
	let v = $derived(rolesState.viewer);

	let draft = $state<Draft | null>(null);
	let draftFor = '';
	let saving = $state(false);
	let nameInput = $state<HTMLInputElement>();

	function fromRole(r: Role): Draft {
		return {
			name: r.name,
			color: hexOk(r.color) ? r.color.toLowerCase() : null,
			permissions: [...r.permissions].sort(),
			irc: { whois_line: r.irc?.whois_line ?? '', vhost: r.irc?.vhost ?? '', oper: !!r.irc?.oper },
			hoist: r.hoist,
			mentionable: r.mentionable
		};
	}
	$effect(() => {
		if (role && draftFor !== role.id) {
			draftFor = role.id;
			draft = fromRole(role);
			members = null;
			if (page.url.searchParams.get('new')) tick().then(() => nameInput?.select());
		}
	});

	let patch = $derived.by(() => {
		if (!role || !draft) return {};
		const base = fromRole(role);
		const out: Record<string, unknown> = {};
		if (draft.name.trim() !== base.name) out.name = draft.name.trim();
		if (draft.color !== base.color) out.color = draft.color;
		if (draft.permissions.join() !== base.permissions.join()) out.permissions = draft.permissions;
		const irc = { ...draft.irc, oper: draft.irc.oper || ircPerms };
		if (JSON.stringify(irc) !== JSON.stringify(base.irc))
			out.irc = {
				oper: irc.oper,
				whois_line: irc.whois_line.trim() || null,
				vhost: irc.vhost.trim() || null
			};
		if (draft.hoist !== base.hoist) out.hoist = draft.hoist;
		if (draft.mentionable !== base.mentionable) out.mentionable = draft.mentionable;
		return out;
	});
	let changes = $derived(Object.keys(patch).length);
	let vhostIssue = $derived(draft ? vhostProblem(draft.irc.vhost) : '');
	let nameIssue = $derived(draft && !role?.builtin ? nameProblem(draft.name) : '');
	// auth makes a role with any irc.* permission an oper class.
	let ircPerms = $derived(
		!!draft?.permissions.some((p) => p.startsWith('irc.')) || role?.builtin === 'owner'
	);

	beforeNavigate((nav) => {
		if (changes && !saving && nav.to?.url.pathname !== page.url.pathname) {
			if (!confirm(`Discard your unsaved changes to ${role?.name}?`)) nav.cancel();
		}
	});

	async function save(e?: Event) {
		e?.preventDefault();
		if (!role || !changes || vhostIssue || nameIssue) return;
		saving = true;
		try {
			const r = await api<{ role: Role }>('PATCH', `/roles/${encodeURIComponent(role.id)}`, patch);
			rolesState.put(r.role);
			draftFor = '';
			toasts.push(`Saved ${r.role.name}.`);
			refreshSession();
		} catch (err) {
			toasts.push(errorText(err), 'bad');
		} finally {
			saving = false;
		}
	}
	function discard() {
		if (role) draft = fromRole(role);
	}

	async function remove() {
		if (!role) return;
		try {
			await api('DELETE', `/roles/${encodeURIComponent(role.id)}`);
			const name = role.name;
			rolesState.remove(role.id);
			draftFor = '';
			draft = null;
			toasts.push(`Deleted ${name}.`);
			refreshSession();
			goto('/roles');
		} catch (err) {
			toasts.push(errorText(err), 'bad');
			throw err;
		}
	}

	function setTab(t: Tab) {
		const u = new URL(page.url);
		u.searchParams.set('tab', t);
		u.searchParams.delete('new');
		goto(u.pathname + u.search, { replaceState: true, noScroll: true, keepFocus: true });
	}
	function tabKey(e: KeyboardEvent) {
		const i = tabs.findIndex((t) => t.id === tab);
		let n = -1;
		if (e.key === 'ArrowRight') n = (i + 1) % tabs.length;
		else if (e.key === 'ArrowLeft') n = (i - 1 + tabs.length) % tabs.length;
		else if (e.key === 'Home') n = 0;
		else if (e.key === 'End') n = tabs.length - 1;
		if (n < 0) return;
		e.preventDefault();
		setTab(tabs[n].id);
		tick().then(() => document.getElementById(`tab-${tabs[n].id}`)?.focus());
	}

	// ---- permissions ----
	const groups: { id: string; label: string; detail: string }[] = [
		{
			id: 'admin',
			label: 'Admin panel',
			detail: 'Which pages of this panel holders can open, and what they can change there.'
		},
		{ id: 'network', label: 'Network', detail: 'Channels and voice in the boson app.' },
		{
			id: 'irc',
			label: 'IRC',
			detail: 'Ergo oper capabilities. They show in /whois and apply on the holder’s next IRC login.'
		}
	];
	let filter = $state('');
	let grouped = $derived.by(() => {
		const f = filter.trim().toLowerCase();
		const match = (p: Permission) =>
			!f || p.label.toLowerCase().includes(f) || p.id.includes(f) || p.description.toLowerCase().includes(f);
		const known = new Set(rolesState.permissions.map((p) => p.id));
		const extra: Permission[] = (draft?.permissions ?? [])
			.filter((p) => !known.has(p))
			.map((p) => ({ id: p, group: 'other', label: p, description: 'Not in this auth service’s list.' }));
		const out = groups.map((g) => ({
			...g,
			items: rolesState.permissions.filter((p) => p.group === g.id && match(p))
		}));
		const rest = rolesState.permissions.filter((p) => !groups.some((g) => g.id === p.group) && match(p));
		if (rest.length || extra.length)
			out.push({
				id: 'other',
				label: 'Other',
				detail: 'Permissions outside the usual groups.',
				items: [...rest, ...extra]
			});
		return out.filter((g) => g.items.length);
	});
	let isOwnerRole = $derived(role?.builtin === 'owner');
	function setPerm(p: string, on: boolean) {
		if (!draft) return;
		const s = new Set(draft.permissions);
		if (on) s.add(p);
		else s.delete(p);
		draft.permissions = [...s].sort();
	}
	let lockedPerms = $derived(rolesState.permissions.filter((p) => permLock(p.id, v)).length);

	// ---- IRC preview ----
	let previewNick = $state(session.info?.user.username ?? 'ana');
	function cloak(nick: string) {
		let h = 2166136261;
		for (const c of nick.toLowerCase()) h = Math.imul(h ^ c.charCodeAt(0), 16777619);
		return (h >>> 0).toString(36).padStart(7, 'x').slice(0, 7) + '.irc';
	}
	let shownHost = $derived(
		draft?.irc.vhost.trim() && !vhostIssue
			? vhostFor(draft.irc.vhost.trim(), previewNick || 'nick')
			: cloak(previewNick || 'nick')
	);
	let ircHost = $derived(session.info?.features.irc_host || 'irc.boson.chat');
	let vhostEl = $state<HTMLInputElement>();
	function insertUsername() {
		if (!draft || !vhostEl) return;
		const el = vhostEl;
		const at = el.selectionStart ?? draft.irc.vhost.length;
		const end = el.selectionEnd ?? at;
		draft.irc.vhost = draft.irc.vhost.slice(0, at) + '{username}' + draft.irc.vhost.slice(end);
		tick().then(() => {
			el.focus();
			el.setSelectionRange(at + 10, at + 10);
		});
	}

	// ---- members ----
	let members = $state<RoleMember[] | null>(null);
	let membersNext = $state('');
	let membersError = $state('');
	let memberBusy = $state('');
	async function loadMembers(cursor = '') {
		if (!role || role.builtin === 'member') return;
		try {
			const q = new URLSearchParams({ limit: '50' });
			if (cursor) q.set('cursor', cursor);
			const r = await api<{ users: RoleMember[]; next_cursor?: string }>(
				'GET',
				`/roles/${encodeURIComponent(role.id)}/members?${q}`
			);
			members = cursor ? [...(members ?? []), ...r.users] : r.users;
			membersNext = r.next_cursor ?? '';
			membersError = '';
		} catch (e) {
			membersError = errorText(e);
		}
	}
	$effect(() => {
		if (tab === 'members' && role && members === null) loadMembers();
	});
	async function addMember(u: User) {
		if (!role) return;
		memberBusy = u.id;
		try {
			await api('PUT', `/roles/${encodeURIComponent(role.id)}/members/${encodeURIComponent(u.id)}`);
			toasts.push(`Gave ${u.username} the ${role.name} role.`);
			await Promise.all([loadMembers(), rolesState.load()]);
			if (u.id === session.info?.user.id) refreshSession();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			memberBusy = '';
		}
	}
	async function removeMember(u: RoleMember) {
		if (!role) return;
		memberBusy = u.id;
		try {
			await api('DELETE', `/roles/${encodeURIComponent(role.id)}/members/${encodeURIComponent(u.id)}`);
			toasts.push(`Removed ${role.name} from ${u.username}.`);
			members = (members ?? []).filter((m) => m.id !== u.id);
			rolesState.load();
			if (u.id === session.info?.user.id) refreshSession();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			memberBusy = '';
		}
	}
</script>

<svelte:head><title>{role?.name ?? 'Role'} · Roles · boson admin</title></svelte:head>

<p class="crumb"><a href="/roles">All roles</a></p>

{#if !role}
	<State kind="error" title="That role doesn’t exist">
		It may have been deleted.
		{#snippet action()}<a class="btn btn-small" href="/roles">All roles</a>{/snippet}
	</State>
{:else if draft}
	<header class="rhead" style:--role={draft.color}>
		<span class="big-dot" class:none={!draft.color} aria-hidden="true"></span>
		<div class="ttl">
			<h2>{draft.name.trim() || 'Untitled role'}</h2>
			<p class="meta">
				{#if role.builtin}<span class="tag">Built-in</span>{/if}
				{#if role.builtin === 'member'}Everyone{:else}{role.member_count}
					{role.member_count === 1 ? 'member' : 'members'}{/if}
				{#if draft.irc.oper}<span class="tag tag-accent" title="Holders are IRC operators">IRC oper</span
					>{/if}
			</p>
		</div>
	</header>

	{#if lock}
		<Notice tone="info">{lock} You can look, but not change it.</Notice>
	{/if}

	<div class="tabs" role="tablist" aria-label="Role settings">
		{#each tabs as t (t.id)}
			<button
				type="button"
				role="tab"
				id="tab-{t.id}"
				aria-selected={tab === t.id}
				aria-controls="panel-{t.id}"
				tabindex={tab === t.id ? 0 : -1}
				onclick={() => setTab(t.id)}
				onkeydown={tabKey}
			>
				{t.label}
				{#if t.id === 'permissions'}<span class="cnt">{isOwnerRole ? 'all' : draft.permissions.length}</span
					>{/if}
			</button>
		{/each}
	</div>

	<div id="panel-{tab}" role="tabpanel" aria-labelledby="tab-{tab}">
		{#if tab === 'channels'}
			<RoleChannels
				roleId={role.builtin === 'member' ? EVERYONE : role.id}
				roleName={role.builtin === 'member' ? 'Everyone' : role.name}
				everyone={role.builtin === 'member'}
				{lock}
			/>
		{:else}
			<form class="panel" onsubmit={save}>
				<fieldset disabled={!!lock} class="fs">
					{#if tab === 'display'}
						<div class="stack">
							<label class="field">
								<span>Name</span>
								<input
									type="text"
									bind:this={nameInput}
									bind:value={draft.name}
									maxlength="32"
									disabled={!!role.builtin}
									aria-invalid={!!nameIssue || undefined}
									aria-describedby="name-hint"
								/>
								<small id="name-hint"
									>{#if role.builtin}Built-in roles keep their names.{:else if nameIssue}{nameIssue}{:else}Up
										to 32 letters, digits, spaces, - and _. It also names the IRC oper class{#if role.slug}
											(<code>{role.slug}</code>){/if}.{/if}</small
								>
							</label>

							<div class="field">
								<span id="color-l">Colour</span>
								<ColorPicker bind:value={draft.color} labelledby="color-l" disabled={!!lock} />
								<small>Used for the role’s name in member lists and on chips like this one.</small>
								<div class="sample">
									<RoleChip role={{ id: role.id, name: draft.name.trim() || 'role', color: draft.color }} />
								</div>
							</div>

							<div class="toggles">
								<Toggle
									label="Show holders separately"
									detail="In the app’s member list, people with this role are grouped under its name."
									bind:checked={draft.hoist}
									disabled={!!lock}
								/>
								<Toggle
									label="Allow anyone to mention this role"
									detail="People can @mention the role to notify everyone who has it."
									bind:checked={draft.mentionable}
									disabled={!!lock}
								/>
							</div>

							<section class="danger" aria-labelledby="del-h">
								<h3 id="del-h">Delete this role</h3>
								{#if role.builtin}
									<p class="muted">
										Built-in roles can’t be deleted. You can rename it, recolour it and change its
										permissions.
									</p>
								{:else}
									<p class="muted">
										{role.member_count === 0
											? 'Nobody has it.'
											: `${role.member_count} ${role.member_count === 1 ? 'person loses' : 'people lose'} it and its permissions straight away, and its oper class on their next IRC login.`}
									</p>
									<Confirm
										title="Delete {role.name}?"
										action="Delete role"
										danger
										typed={role.name}
										onconfirm={remove}
									>
										{#snippet trigger(open)}
											<button type="button" class="btn btn-danger btn-small" onclick={open} disabled={!!lock}
												>Delete role</button
											>
										{/snippet}
										This can’t be undone. Its WHOIS line and vhost go with it.
									</Confirm>
								{/if}
							</section>
						</div>
					{:else if tab === 'permissions'}
						<div class="perm-tools">
							<label class="search">
								<span class="sr-only">Filter permissions</span>
								<input
									type="search"
									placeholder="Filter permissions"
									bind:value={filter}
									autocomplete="off"
									spellcheck="false"
								/>
							</label>
							{#if !isOwnerRole && !lock}
								<span class="muted small">{draft.permissions.length} on</span>
							{/if}
						</div>
						{#if isOwnerRole}
							<Notice tone="info"
								>Owner has every permission and outranks every role. Its permissions can’t be narrowed.</Notice
							>
						{:else if !lock && lockedPerms && !v.owner}
							<Notice tone="info">
								{lockedPerms === 1 ? 'One permission is' : `${lockedPerms} permissions are`} locked: you can only
								grant permissions you have yourself.
							</Notice>
						{/if}
						{#each grouped as g (g.id)}
							<section class="pgroup" aria-labelledby="pg-{g.id}">
								<div class="pg-head">
									<h3 id="pg-{g.id}">{g.label}</h3>
									<p>{g.detail}</p>
									{#if g.id === 'irc' && !isOwnerRole}
										<p class="warn-line">
											{#if ircPerms}Holders are IRC operators (oper class <code>{role.slug ?? role.name}</code
												>) while any of these is on.{:else}Turning any of these on makes holders IRC
												operators.{/if}
										</p>
									{/if}
								</div>
								<ul>
									{#each g.items as p (p.id)}
										{@const why = isOwnerRole ? 'Owner has every permission.' : lock || permLock(p.id, v)}
										<li title={why || undefined}>
											<Toggle
												label={p.label}
												detail={p.description}
												checked={isOwnerRole || draft.permissions.includes(p.id)}
												disabled={!!why}
												onchange={(on) => setPerm(p.id, on)}
											/>
											{#if p.label !== p.id}<code class="pid">{p.id}</code>{/if}
										</li>
									{/each}
								</ul>
							</section>
						{:else}
							<p class="muted">No permission matches “{filter}”.</p>
						{/each}
					{:else if tab === 'irc'}
						<div class="irc">
							<div class="stack">
								<div class="toggles">
									<Toggle
										label="Oper up on IRC"
										detail={ircPerms
											? 'Always on while the role has IRC permissions. Holders get them and the WHOIS line on their next IRC login.'
											: 'Holders become IRC operators with this role’s WHOIS line. It takes effect on their next IRC login.'}
										checked={draft.irc.oper || ircPerms}
										onchange={(on) => draft && (draft.irc.oper = on)}
										disabled={!!lock || ircPerms}
									/>
								</div>
								<label class="field">
									<span>WHOIS line</span>
									<span class="affix">
										<span class="pre" aria-hidden="true">{previewNick || 'nick'}</span>
										<input
											type="text"
											bind:value={draft.irc.whois_line}
											maxlength="120"
											placeholder={defaultWhois(draft.name || role.name)}
											aria-describedby="whois-hint"
											spellcheck="true"
										/>
									</span>
									<small id="whois-hint"
										>What <code>/whois</code> says about holders, after their nick. Empty uses “{defaultWhois(
											draft.name || role.name
										)}”. {draft.irc.whois_line.length}/120.</small
									>
								</label>
								<label class="field">
									<span>Vhost</span>
									<span class="affix">
										<input
											type="text"
											bind:this={vhostEl}
											bind:value={draft.irc.vhost}
											maxlength="80"
											placeholder={'{username}.staff.' + ircHost.replace(/^irc\./, '')}
											spellcheck="false"
											autocapitalize="off"
											aria-invalid={!!vhostIssue || undefined}
											aria-describedby="vhost-hint"
										/>
										<button type="button" class="btn btn-small ins" onclick={insertUsername} disabled={!!lock}
											>{'{username}'}</button
										>
									</span>
									<small id="vhost-hint" class:bad={!!vhostIssue}>
										{#if vhostIssue}{vhostIssue}{:else}Replaces the hidden host in their hostmask, so everyone
											on IRC sees the role. Leave it empty to keep the server’s cloak.{/if}
									</small>
								</label>
								<label class="field narrow">
									<span>Preview as</span>
									<input
										type="text"
										bind:value={previewNick}
										maxlength="30"
										spellcheck="false"
										autocapitalize="off"
									/>
								</label>
							</div>
							<WhoisPreview
								nick={previewNick}
								host={shownHost}
								whois={draft.irc.whois_line.trim() || defaultWhois(draft.name || role.name)}
								oper={draft.irc.oper || ircPerms}
								server={ircHost}
							/>
						</div>
					{:else if tab === 'members'}
						{#if role.builtin === 'member'}
							<State kind="empty" title="Everyone is a Member">
								Member is the role every account has. Its permissions are the baseline for the whole network;
								give people more with other roles.
							</State>
						{:else}
							<div class="add">
								<UserSearch
									label="Add a member"
									placeholder="Add someone: search by username or email"
									exclude={members?.map((m) => m.id) ?? []}
									onpick={addMember}
									disabled={!!lock || !!memberBusy}
								/>
							</div>
							{#if membersError}
								<State kind="error">
									{membersError}
									{#snippet action()}<button type="button" class="btn btn-small" onclick={() => loadMembers()}
											>Try again</button
										>{/snippet}
								</State>
							{:else if members === null}
								<State kind="loading" title="Loading members…" />
							{:else if members.length === 0}
								<State kind="empty" title="Nobody has {role.name} yet"
									>Search above to give it to someone.</State
								>
							{:else}
								<ul class="members">
									{#each members as m (m.id)}
										<li>
											<div class="who">
												{#if session.can('users.view')}<a class="uname" href="/users/{m.id}">{m.username}</a
													>{:else}<span class="uname">{m.username}</span>{/if}
												{#if m.display_name && m.display_name !== m.username}<span class="muted small"
														>{m.display_name}</span
													>{/if}
											</div>
											<div class="others muted small">
												{#if m.disabled}<span class="tag tag-bad">Disabled</span>{/if}
												{#if m.assigned_at}Since <Time at={m.assigned_at} />{/if}
											</div>
											<button
												type="button"
												class="btn btn-small btn-ghost"
												onclick={() => removeMember(m)}
												disabled={!!lock || memberBusy === m.id}
												aria-label="Remove {role.name} from {m.username}"
												>{memberBusy === m.id ? 'Removing…' : 'Remove'}</button
											>
										</li>
									{/each}
								</ul>
								{#if membersNext}
									<button type="button" class="btn btn-small more" onclick={() => loadMembers(membersNext)}
										>Show more</button
									>
								{/if}
							{/if}
						{/if}
					{/if}
				</fieldset>

				{#if tab !== 'members' && !lock}
					<div class="bar" class:dirty={changes > 0}>
						<p class="meta" aria-live="polite">
							{#if changes}{changes === 1 ? 'One unsaved change' : `${changes} unsaved changes`}{:else}All
								changes saved{/if}
						</p>
						<div class="btns">
							<button class="btn btn-ghost" type="button" disabled={!changes || saving} onclick={discard}
								>Discard</button
							>
							<button class="btn btn-primary" disabled={!changes || saving || !!vhostIssue || !!nameIssue}
								>{saving ? 'Saving…' : 'Save changes'}</button
							>
						</div>
					</div>
				{/if}
			</form>
		{/if}
	</div>
{/if}

<style>
	.crumb {
		display: none;
		margin-bottom: var(--space-4);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
	}
	.rhead {
		--role: var(--fg-3);
		display: flex;
		align-items: center;
		gap: var(--space-4);
		margin-bottom: var(--space-5);
	}
	.big-dot {
		flex: none;
		width: 36px;
		height: 36px;
		border-radius: 50%;
		background: var(--role);
		box-shadow:
			0 0 0 4px var(--bg),
			0 0 0 5px color-mix(in oklab, var(--role), transparent 50%);
	}
	.big-dot.none {
		background: var(--surface);
		box-shadow: inset 0 0 0 2px var(--border-strong);
	}
	.ttl {
		min-width: 0;
	}
	.ttl h2 {
		font-size: var(--text-xl);
		overflow-wrap: anywhere;
	}
	.meta {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		margin-top: var(--space-1);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.tabs {
		display: flex;
		gap: var(--space-1);
		margin: var(--space-5) 0 var(--space-6);
		border-bottom: 1px solid var(--border);
		overflow-x: auto;
		scrollbar-width: none;
	}
	[role='tab'] {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		padding: var(--space-2) var(--space-3);
		margin-bottom: -1px;
		border: 0;
		border-bottom: 2px solid transparent;
		background: none;
		color: var(--fg-quiet);
		font-family: var(--font-display);
		font-size: var(--text-sm);
		font-weight: 600;
		cursor: pointer;
		white-space: nowrap;
	}
	[role='tab']:hover {
		color: var(--fg);
	}
	[role='tab'][aria-selected='true'] {
		color: var(--fg);
		border-bottom-color: var(--accent);
	}
	.cnt {
		font-family: var(--font-mono);
		font-size: 10px;
		padding: 0 5px;
		border-radius: 999px;
		background: var(--surface-hover);
		color: var(--fg-quiet);
	}
	.fs {
		border: 0;
		margin: 0;
		padding: 0;
		min-width: 0;
	}
	.panel {
		display: grid;
		gap: var(--space-6);
	}
	.stack {
		display: grid;
		gap: var(--space-6);
		max-width: 640px;
		align-content: start;
	}
	.sample {
		margin-top: var(--space-1);
	}
	.toggles {
		display: grid;
		gap: var(--space-4);
		padding: var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.danger {
		display: grid;
		gap: var(--space-2);
		justify-items: start;
		padding-top: var(--space-5);
		border-top: 1px solid var(--border-soft);
	}
	.danger p {
		font-size: var(--text-sm);
	}
	.perm-tools {
		display: flex;
		align-items: center;
		gap: var(--space-4);
		margin-bottom: var(--space-4);
	}
	.search {
		flex: 1 1 240px;
		max-width: 360px;
	}
	.small {
		font-size: var(--text-xs);
		font-family: var(--font-mono);
	}
	.pgroup {
		margin-top: var(--space-8);
		max-width: 760px;
	}
	.pgroup:first-of-type {
		margin-top: var(--space-5);
	}
	.pg-head {
		display: grid;
		gap: var(--space-1);
		margin-bottom: var(--space-3);
	}
	.pg-head p {
		color: var(--fg-quiet);
		font-size: var(--text-sm);
	}
	.warn-line {
		color: var(--warn-text) !important;
	}
	.warn-line code {
		color: var(--fg);
	}
	.pgroup ul {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.pgroup li {
		display: grid;
		gap: var(--space-1);
		padding: var(--space-3) var(--space-4);
	}
	.pgroup li + li {
		border-top: 1px solid var(--border-soft);
	}
	.pid {
		justify-self: start;
		font-size: 10px;
		color: var(--fg-quiet);
		background: none;
		padding: 0;
	}
	.irc {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 1.05fr);
		gap: var(--space-8);
		align-items: start;
	}
	.affix {
		display: flex;
		align-items: stretch;
		min-width: 0;
	}
	.affix .pre {
		display: inline-flex;
		align-items: center;
		padding: 0 var(--space-3);
		border: 1px solid var(--border);
		border-right: 0;
		border-radius: var(--radius-sm) 0 0 var(--radius-sm);
		background: var(--surface-raised);
		color: var(--fg-quiet);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		max-width: 40%;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		/* the label span styles must not apply */
		text-transform: none;
		letter-spacing: 0;
	}
	.affix .pre + input {
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
	}
	.affix .ins {
		flex: none;
		margin-left: var(--space-2);
		min-height: 38px;
		font-family: var(--font-mono);
		text-transform: none;
	}
	.narrow {
		max-width: 240px;
	}
	small.bad {
		color: var(--danger-text);
	}
	input[aria-invalid='true'] {
		border-color: var(--danger);
	}
	.add {
		max-width: 520px;
		margin-bottom: var(--space-4);
	}
	.members {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
		max-width: 760px;
	}
	.members li {
		display: grid;
		grid-template-columns: minmax(120px, 1fr) minmax(0, 1.4fr) auto;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-2) var(--space-2) var(--space-2) var(--space-4);
	}
	.members li + li {
		border-top: 1px solid var(--border-soft);
	}
	.who {
		display: grid;
		min-width: 0;
	}
	.uname {
		font-family: var(--font-mono);
		font-weight: 600;
		color: var(--fg);
		text-decoration: none;
		overflow-wrap: anywhere;
	}
	a.uname:hover {
		color: var(--accent-text);
		text-decoration: underline;
	}
	.others {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
		min-width: 0;
	}
	.more {
		margin-top: var(--space-3);
	}
	.bar {
		position: sticky;
		bottom: 0;
		z-index: 5;
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3) var(--space-4);
		align-items: center;
		justify-content: space-between;
		padding: var(--space-3) var(--space-4);
		margin: 0 calc(-1 * var(--space-4));
		background: color-mix(in oklab, var(--bg), transparent 8%);
		backdrop-filter: blur(8px);
		border-top: 1px solid var(--border);
	}
	.bar.dirty {
		border-top-color: var(--accent);
	}
	.bar .meta {
		margin: 0;
	}
	.dirty .meta {
		color: var(--accent-text);
	}
	.btns {
		display: flex;
		gap: var(--space-2);
		margin-left: auto;
	}
	@media (max-width: 1180px) {
		.irc {
			grid-template-columns: minmax(0, 1fr);
		}
	}
	@media (max-width: 980px) {
		.crumb {
			display: block;
		}
	}
	@media (max-width: 560px) {
		.members li {
			grid-template-columns: minmax(0, 1fr) auto;
		}
		.others {
			grid-column: 1 / -1;
			grid-row: 2;
		}
	}
</style>
