<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import {
		DEFAULT_DIRECTORY,
		DOCS_URL,
		FIELD_LABELS,
		MODES,
		changes,
		draftFrom,
		homeSummary,
		sourceText,
		splitIdentity,
		validate,
		type BlockResult,
		type Draft,
		type Field,
		type FederationView,
		type RemoteUser,
		type RemoteUserPage
	} from '$lib/federation';
	import ChoiceGroup from '$lib/components/ChoiceGroup.svelte';
	import Confirm from '$lib/components/Confirm.svelte';
	import DomainList from '$lib/components/DomainList.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';
	import Toggle from '$lib/components/Toggle.svelte';

	let view = $state<FederationView | null>(null);
	let loadError = $state('');
	let saved = $state<Draft | null>(null);
	let draft = $state<Draft | null>(null);
	let saving = $state(false);
	let checking = $state(false);
	let resetting = $state<Field | ''>('');
	let serverError = $state<{ field: Field; message: string } | null>(null);

	let errors = $derived(draft ? validate(draft) : {});
	let shownErrors = $derived.by(() => {
		const out: Partial<Record<Field, string>> = { ...errors };
		if (serverError && !out[serverError.field]) out[serverError.field] = serverError.message;
		return out;
	});
	let hasErrors = $derived(Object.keys(errors).length > 0);
	let change = $derived(draft && saved ? changes(draft, saved) : {});
	let count = $derived(Object.keys(change).length);
	let dirty = $derived(count > 0);
	let editable = $derived(!!view?.editable);
	let newBlocks = $derived(draft && saved ? draft.block.filter((d) => !saved!.block.includes(d)) : []);
	let mode = $derived(MODES.find((m) => m.value === draft?.mode));
	let home = $derived(view?.home);
	let summary = $derived(homeSummary(home));
	let envLines = $derived(
		(view?.fields ?? [])
			.map((f) => {
				const v = view!.settings[f.name];
				return `${f.env}=${Array.isArray(v) ? v.join(',') : String(v ?? '')}`;
			})
			.join('\n')
	);
	let envOf = $derived(
		Object.fromEntries((view?.fields ?? []).map((f) => [f.name, f.env])) as Record<Field, string>
	);

	function fill(v: FederationView) {
		view = v;
		saved = draftFrom(v);
		draft = draftFrom(v);
		serverError = null;
	}

	async function load(fresh = false) {
		try {
			fill(await api<FederationView>('GET', '/federation' + (fresh ? '?fresh=1' : '')));
			loadError = '';
		} catch (e) {
			loadError = errorText(e);
		}
	}

	async function recheck() {
		checking = true;
		try {
			const v = await api<FederationView>('GET', '/federation?fresh=1');
			// Keep unsaved edits; only the status is new.
			if (view) view = { ...view, home: v.home };
			else fill(v);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			checking = false;
		}
	}

	const fieldNames = new Set<string>([
		'mode',
		'allow',
		'block',
		'directory_url',
		'remote_upload_quota',
		'remote_voice'
	]);
	function fieldOf(err: unknown): Field | null {
		if (!(err instanceof ApiError)) return null;
		if (err.field && fieldNames.has(err.field)) return err.field as Field;
		const m = /auth\.federation\.([a-z_]+)/.exec(err.message);
		return m && fieldNames.has(m[1]) ? (m[1] as Field) : null;
	}

	async function save() {
		if (!dirty || hasErrors || !draft) return;
		saving = true;
		serverError = null;
		const blocks = [...newBlocks];
		try {
			let v = await api<FederationView>('PATCH', '/federation', change);
			// Newly blocked servers: end their users' sessions now (the
			// list already has them, so this only signs them out).
			let ended = 0;
			for (const d of blocks) {
				const r = await api<BlockResult>('POST', '/federation/blocked-domains', { domain: d });
				ended += r.revoked;
				v = r.view;
				if (r.revoke_error)
					toasts.push(`${d} is blocked, but its sessions couldn’t be ended: ${r.revoke_error}`, 'bad');
			}
			fill(v);
			toasts.push(
				blocks.length
					? `Federation saved. ${ended === 1 ? 'One session' : `${ended} sessions`} ended.`
					: view?.store === 'file'
						? 'Federation saved. Auth picks it up within seconds.'
						: 'Federation saved.'
			);
			if (blocks.length) void loadUsers();
		} catch (err) {
			const f = fieldOf(err);
			if (f && err instanceof Error) serverError = { field: f, message: err.message };
			toasts.push(errorText(err), 'bad');
			throw err;
		} finally {
			saving = false;
		}
	}
	function submit(e: Event) {
		e.preventDefault();
		if (!newBlocks.length) save().catch(() => {});
	}
	function discard() {
		if (saved) draft = draftFrom({ ...view!, settings: saved });
		serverError = null;
	}
	async function resetField(f: Field) {
		resetting = f;
		try {
			fill(await api<FederationView>('PATCH', '/federation', { unset: [f] }));
			toasts.push(`${FIELD_LABELS[f]} is back to its default.`);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			resetting = '';
		}
	}

	// ---- remote users ----
	let users = $state<RemoteUser[]>([]);
	let total = $state(0);
	let next = $state('');
	let usersError = $state('');
	let usersLoading = $state(false);
	let q = $state('');
	let reason = $state('');
	let timer: ReturnType<typeof setTimeout> | undefined;

	async function loadUsers(more = false) {
		if (!view?.supported) return;
		usersLoading = true;
		try {
			const params = new URLSearchParams();
			if (q.trim()) params.set('q', q.trim());
			if (more && next) params.set('cursor', next);
			const p = await api<RemoteUserPage>('GET', '/federation/users' + (params.size ? '?' + params : ''));
			users = more ? [...users, ...p.users] : p.users;
			total = p.total;
			next = p.next_cursor ?? '';
			usersError = '';
		} catch (e) {
			usersError = errorText(e);
		} finally {
			usersLoading = false;
		}
	}
	function search() {
		clearTimeout(timer);
		timer = setTimeout(() => loadUsers(), 250);
	}
	function replace(u: RemoteUser) {
		users = users.map((x) => (x.id === u.id ? { ...x, ...u } : x));
	}

	async function ban(u: RemoteUser) {
		try {
			replace(
				await api<RemoteUser>('POST', `/federation/users/${encodeURIComponent(u.id)}/ban`, {
					reason: reason.trim()
				})
			);
			toasts.push(`${u.identity} is banned here.`);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			throw e;
		}
	}
	async function unban(u: RemoteUser) {
		try {
			replace(await api<RemoteUser>('DELETE', `/federation/users/${encodeURIComponent(u.id)}/ban`));
			toasts.push(`${u.identity} can sign in again.`);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			throw e;
		}
	}
	async function blockDomain(domain: string) {
		try {
			const r = await api<BlockResult>('POST', '/federation/blocked-domains', { domain });
			// Unsaved edits stay; the lists follow what was saved.
			const keep = draft && saved ? changes(draft, saved) : {};
			fill(r.view);
			if (draft) Object.assign(draft, keep, { block: r.view.settings.block, allow: r.view.settings.allow });
			toasts.push(
				r.revoke_error
					? `${domain} is blocked, but its sessions couldn’t be ended: ${r.revoke_error}`
					: `${domain} is blocked. ${r.revoked === 1 ? 'One session' : `${r.revoked} sessions`} ended.`,
				r.revoke_error ? 'bad' : undefined
			);
			await loadUsers();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			throw e;
		}
	}
	async function unblockDomain(domain: string) {
		try {
			fill(await api<FederationView>('DELETE', `/federation/blocked-domains/${encodeURIComponent(domain)}`));
			toasts.push(`${domain} is off the blocklist.`);
			await loadUsers();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
	}
	let peopleFrom = (d: string) => users.filter((u) => u.home_domain === d);

	onMount(async () => {
		await load();
		void loadUsers();
	});

	const errId = (f: Field) => `fed-${f}-err`;
	const stateTag: Record<string, string> = { ok: 'tag-ok', warn: 'tag-warn', bad: 'tag-bad', off: '' };
</script>

<svelte:head><title>Federation · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Federation</h1>
		<p>
			People with an account on another Boson server can sign in here as <code>alice@boson.chat</code>,
			without making a new account. Their password and chats stay on their home server; this server only gets
			a signed, five-minute proof of who they are. Here they follow your rules, quotas and bans.
		</p>
		<p class="docs">
			<a href={DOCS_URL} target="_blank" rel="noreferrer">How it works: auth docs/federation.md</a>
		</p>
	</div>
</div>

{#if loadError && !view}
	<State kind="error">
		{loadError}
		{#snippet action()}<button class="btn btn-small" onclick={() => load()}>Try again</button>{/snippet}
	</State>
{:else if !view || !draft}
	<State kind="loading" title="Loading federation…" />
{:else}
	<div class="notes">
		{#if view.requires}
			<Notice tone="warn">
				<strong>This auth service doesn’t do federation yet</strong> (it needs auth ≥ {view.requires}).
				{#if view.store === 'file'}You can save the settings now; they apply once auth is updated.{/if}
				The remote users list appears after the update.
			</Notice>
		{:else if view.error}
			<Notice tone="bad">Couldn’t read auth’s federation status: {view.error}</Notice>
		{/if}
		{#if view.store === 'env'}
			<Notice tone="info">
				<strong>These settings come from auth’s environment.</strong> To change them, set them in the stack’s
				<code>.env</code> and run <code>docker compose up -d auth</code>:
				<code class="block">{envLines}</code>
				Or set <code>BOSON_CONFIG</code> on the admin service to change them here.
			</Notice>
		{:else if !session.can('settings.manage')}
			<Notice tone="info"
				>You can look but not change these: saving needs <code>settings.manage</code>.</Notice
			>
		{/if}
	</div>

	<div class="layout">
		<form class="form" onsubmit={submit} novalidate>
			<fieldset disabled={!editable}>
				<legend class="sr-only">Federation settings</legend>

				<section class="group" aria-labelledby="fed-mode-h">
					<div class="group-head">
						<h2 id="fed-mode-h">Who can sign in from other servers</h2>
						<p>
							Every mode checks the home server’s signature, refuses your blocklist and banned people, and
							gives remote users your rules. The mode decides which home servers you trust at all.
						</p>
					</div>
					<div class="card fields">
						<ChoiceGroup legend="Mode" name="fed-mode" choices={MODES} bind:value={draft.mode} />
						{#if mode}
							<p class="risk" data-mode={mode.value} data-testid="mode-risk">
								<span class="risk-l">Tradeoff</span>
								{mode.risk}
							</p>
						{/if}
						{@render source('mode')}
					</div>
				</section>

				<section class="group" aria-labelledby="fed-lists-h">
					<div class="group-head">
						<h2 id="fed-lists-h">Home servers</h2>
						<p>
							By domain: the part after the @ in <code>alice@boson.chat</code>. Paste an identity or a URL and
							the domain is kept.
						</p>
					</div>
					<div class="card fields">
						<div class="field" class:dim={draft.mode !== 'allowlist'}>
							<span class="lbl" id="fed-allow-l">Allowlist</span>
							<DomainList
								id="fed-allow"
								label="allowlist"
								bind:items={draft.allow}
								other={{ name: 'blocklist', list: draft.block }}
								empty={draft.mode === 'allowlist'
									? 'Nobody yet. Add the servers whose users may sign in.'
									: 'Empty.'}
								disabled={!editable}
								error={shownErrors.allow}
							/>
							<small
								>{draft.mode === 'allowlist'
									? 'Only these servers’ users can sign in.'
									: 'Only used in Allowlist mode.'}
								{@render sourceInline('allow')}</small
							>
						</div>
						<div class="field">
							<span class="lbl" id="fed-block-l">Blocklist</span>
							<DomainList
								id="fed-block"
								label="blocklist"
								bind:items={draft.block}
								other={{ name: 'allowlist', list: draft.allow }}
								placeholder="spam.example"
								empty="No blocked servers."
								disabled={!editable}
								error={shownErrors.block}
							/>
							<small
								>Refused in every mode. Saving a new block also signs out everyone from that server.
								{@render sourceInline('block')}</small
							>
						</div>
					</div>
				</section>

				<section class="group" aria-labelledby="fed-remote-h">
					<div class="group-head">
						<h2 id="fed-remote-h">What remote users get</h2>
						<p>
							Remote users chat on your IRC server under their handle here and upload files to your server.
							They get the <code>remote</code> role, plus any roles you give them.
						</p>
					</div>
					<div class="card fields">
						<div class="field quota">
							<label class="lbl" for="fed-quota">Upload quota</label>
							<input
								id="fed-quota"
								type="text"
								bind:value={draft.remote_upload_quota}
								placeholder="Same as accounts"
								autocomplete="off"
								spellcheck="false"
								aria-invalid={!!shownErrors.remote_upload_quota}
								aria-describedby="fed-quota-hint{shownErrors.remote_upload_quota
									? ' ' + errId('remote_upload_quota')
									: ''}"
							/>
							{#if shownErrors.remote_upload_quota}<p class="err" id={errId('remote_upload_quota')}>
									{shownErrors.remote_upload_quota}
								</p>{/if}
							<small id="fed-quota-hint"
								>Per remote user, over the uploads quota window, like <code>500MB</code>. Empty: the same as
								your own accounts. {@render sourceInline('remote_upload_quota')}</small
							>
						</div>
						<div class="tgl">
							<Toggle
								label="Remote users can join voice"
								detail="And so video. Off: they can chat and upload, but voice rooms refuse them. Voice uses your server’s bandwidth."
								bind:checked={draft.remote_voice}
							/>
							{@render source('remote_voice')}
						</div>
					</div>
				</section>

				<details
					class="group adv"
					open={draft.directory_url !== DEFAULT_DIRECTORY || !!shownErrors.directory_url}
				>
					<summary><h2>Advanced</h2></summary>
					<div class="card fields">
						<div class="field" class:dim={draft.mode !== 'directory'}>
							<label class="lbl" for="fed-dir">Directory</label>
							<input
								id="fed-dir"
								type="text"
								inputmode="url"
								bind:value={draft.directory_url}
								placeholder={DEFAULT_DIRECTORY}
								autocomplete="off"
								spellcheck="false"
								aria-invalid={!!shownErrors.directory_url}
								aria-describedby="fed-dir-hint{shownErrors.directory_url ? ' ' + errId('directory_url') : ''}"
							/>
							{#if shownErrors.directory_url}<p class="err" id={errId('directory_url')}>
									{shownErrors.directory_url}
								</p>{/if}
							<small id="fed-dir-hint"
								>In Verified servers mode, a home server must have a verified listing here. Point it at your
								own directory to trust only what it verifies. {@render sourceInline('directory_url')}</small
							>
						</div>
					</div>
				</details>
			</fieldset>

			<div class="bar" class:dirty>
				<p class="meta" aria-live="polite">
					{#if dirty && hasErrors}
						Fix the highlighted fields to save
					{:else if dirty}
						{count === 1 ? 'One unsaved change' : `${count} unsaved changes`}
					{:else if view.store === 'file'}
						Saved to <code>auth.federation</code> in config/boson.yaml
					{:else if view.store === 'auth'}
						Saved in auth’s database
					{:else}
						Read-only: from auth’s env
					{/if}
				</p>
				<div class="btns">
					<button class="btn btn-ghost" type="button" disabled={!dirty || saving} onclick={discard}
						>Discard</button
					>
					{#if newBlocks.length}
						<Confirm
							title="Block {newBlocks.length === 1 ? newBlocks[0] : `${newBlocks.length} servers`}?"
							action="Save and sign them out"
							danger
							onconfirm={save}
						>
							{#snippet trigger(open)}
								<button
									class="btn btn-primary"
									type="button"
									disabled={hasErrors || saving || !editable}
									onclick={open}>{saving ? 'Saving…' : 'Save changes'}</button
								>
							{/snippet}
							<p>
								Everyone from {newBlocks.join(', ')} is signed out of this server now, and can’t sign in again while
								it’s blocked. Their messages stay.
							</p>
						</Confirm>
					{:else}
						<button class="btn btn-primary" disabled={!dirty || hasErrors || saving || !editable}
							>{saving ? 'Saving…' : 'Save changes'}</button
						>
					{/if}
				</div>
			</div>
		</form>

		<aside class="side" aria-labelledby="fed-home-h">
			<div class="card status" data-tone={summary.tone} data-testid="home-status">
				<div class="st-head">
					<h2 id="fed-home-h">Your server as a home</h2>
					<button class="btn btn-small btn-ghost" type="button" onclick={recheck} disabled={checking}
						>{checking ? 'Checking…' : 'Check again'}</button
					>
				</div>
				<p class="lead">
					<span class="tag {stateTag[summary.tone]}"
						><span class="dot" aria-hidden="true"></span>{summary.title}</span
					>
				</p>
				<p class="small">{summary.text}</p>
				{#if home?.checks.length}
					<ul class="checks">
						{#each home.checks as c (c.id)}
							<li class:bad={!c.ok && !c.warn} class:warn={!c.ok && c.warn}>
								<span class="mark" aria-hidden="true">{c.ok ? '✓' : c.warn ? '!' : '×'}</span>
								<span
									><span class="sr-only">{c.ok ? 'OK:' : c.warn ? 'Warning:' : 'Problem:'}</span>
									{c.label}{#if c.detail}<span class="detail">{c.detail}</span>{/if}</span
								>
							</li>
						{/each}
					</ul>
				{/if}
				{#if home?.error && home.state !== 'unknown'}<p class="problem small">{home.error}</p>{/if}
				<dl>
					{#if view.domain || home?.domain}
						<dt>Domain</dt>
						<dd class="wrap">{view.domain || home?.domain}</dd>
					{/if}
					{#if view.issuer}
						<dt>Issuer</dt>
						<dd class="wrap">{view.issuer}</dd>
					{/if}
					{#if home?.federation}
						<dt>Accepts</dt>
						<dd>{home.federation.accepts}</dd>
					{/if}
				</dl>
				{#if home?.public_url}
					<p class="test">
						<a href={home.public_url} target="_blank" rel="noreferrer" data-testid="home-link"
							>Open boson.json ↗</a
						>
						<span class="muted small">what other servers read</span>
					</p>
				{/if}
				{#if home}<p class="checked small muted">Checked <Time at={home.checked_at} /></p>{/if}
			</div>
		</aside>
	</div>

	<section class="section users" aria-labelledby="fed-users-h">
		<div class="section-head">
			<div>
				<h2 id="fed-users-h">Remote users</h2>
				<p>People from other servers who signed in here. Newest activity first.</p>
			</div>
		</div>
		{#if !view.supported}
			<State kind="unsupported" title="Needs a newer auth service">
				The list of remote users comes with auth ≥ {view.requires ?? 'the federation release'}.
			</State>
		{:else}
			<div class="toolbar">
				<label class="search">
					<span class="sr-only">Search remote users</span>
					<input
						type="search"
						placeholder="Search by identity or handle"
						bind:value={q}
						oninput={search}
						autocomplete="off"
						spellcheck="false"
					/>
				</label>
				<span class="muted count" aria-live="polite"
					>{#if !usersLoading || users.length}{total === 0
							? 'No remote users'
							: total === 1
								? '1 remote user'
								: `${total} remote users`}{/if}</span
				>
			</div>
			{#if usersError}
				<Notice>{usersError}</Notice>
			{:else}
				<div class="table-wrap" aria-busy={usersLoading}>
					<table>
						<thead>
							<tr>
								<th>Identity</th>
								<th class="hide-s">Handle here</th>
								<th class="hide-m">First seen</th>
								<th class="hide-s">Last seen</th>
								<th><span class="sr-only">Actions</span></th>
							</tr>
						</thead>
						<tbody>
							{#each users as u (u.id)}
								{@const [name, domain] = splitIdentity(u.identity)}
								<tr class:banned={u.banned || u.domain_blocked} data-testid="remote-{u.id}">
									<td>
										<span class="ident"
											><span class="who">{name}</span><wbr /><span class="at">@{domain || u.home_domain}</span
											></span
										>
										{#if u.display_name && u.display_name !== name}<div class="muted small">
												{u.display_name}
											</div>{/if}
										<div class="badges">
											{#if u.banned}<span class="tag tag-bad" title={u.ban_reason || undefined}>Banned</span
												>{/if}
											{#if u.domain_blocked}<span class="tag tag-bad">Server blocked</span>{/if}
											<span class="show-s muted small"
												>as {u.handle} · seen <Time at={u.last_seen_at} empty="never" compact /></span
											>
										</div>
									</td>
									<td class="hide-s mono">
										{#if session.can('users.view')}<a href="/users/{u.id}">{u.handle}</a
											>{:else}{u.handle}{/if}
									</td>
									<td class="hide-m"><Time at={u.first_seen_at} /></td>
									<td class="seen hide-s"><Time at={u.last_seen_at} empty="Never" compact /></td>
									<td class="acts">
										{#if session.can('users.moderate') || session.can('users.manage')}
											{#if u.banned}
												<Confirm title="Unban {u.identity}?" action="Unban" onconfirm={() => unban(u)}>
													{#snippet trigger(open)}<button class="btn btn-small" onclick={open}>Unban</button
														>{/snippet}
													<p>They can sign in here again with their {u.home_domain} account.</p>
												</Confirm>
											{:else}
												<Confirm
													title="Ban {u.identity}?"
													action="Ban"
													danger
													onconfirm={() => ban(u)}
													oncancel={() => (reason = '')}
												>
													{#snippet trigger(open)}<button
															class="btn btn-small"
															onclick={() => {
																reason = '';
																open();
															}}>Ban</button
														>{/snippet}
													<p>
														{u.identity} is signed out here and can’t sign in again. Other people from
														{u.home_domain} aren’t affected.
													</p>
													<label class="reason">
														<span>Reason (optional, for the audit log)</span>
														<input type="text" bind:value={reason} maxlength="200" />
													</label>
												</Confirm>
											{/if}
										{/if}
										{#if editable}
											{#if u.domain_blocked}
												<button class="btn btn-small btn-ghost" onclick={() => unblockDomain(u.home_domain)}
													>Unblock {u.home_domain}</button
												>
											{:else}
												<Confirm
													title="Block {u.home_domain}?"
													action="Block and sign them out"
													danger
													onconfirm={() => blockDomain(u.home_domain)}
												>
													{#snippet trigger(open)}<button class="btn btn-small btn-ghost" onclick={open}
															>Block server</button
														>{/snippet}
													<p>
														Everyone from <strong>{u.home_domain}</strong> is signed out of this server now
														{#if peopleFrom(u.home_domain).length > 1}({peopleFrom(u.home_domain).length} people
															listed here){/if}
														and their sessions end. Nobody from {u.home_domain} can sign in again until you unblock
														it. Their messages stay.
													</p>
												</Confirm>
											{/if}
										{/if}
									</td>
								</tr>
							{:else}
								{#if !usersLoading}
									<tr>
										<td colspan="5" class="empty">
											{#if q.trim()}No remote user matches “{q.trim()}”.
											{:else if draft.mode === 'off'}Federation is off, so nobody from another server can sign
												in.
											{:else}Nobody from another server has signed in yet.{/if}
										</td>
									</tr>
								{/if}
							{/each}
						</tbody>
					</table>
				</div>
				{#if next}
					<div class="pager">
						<button class="btn btn-small" disabled={usersLoading} onclick={() => loadUsers(true)}
							>Show more</button
						>
					</div>
				{/if}
			{/if}
		{/if}
	</section>
{/if}

{#snippet sourceInline(f: Field)}
	{#if view}
		<span class="src"
			>{sourceText(view.sources[f], envOf[f])}{#if view.store === 'file' && view.sources[f] === 'file'}
				·
				<button
					class="linkish"
					type="button"
					disabled={resetting === f || !editable}
					onclick={() => resetField(f)}>{resetting === f ? 'Resetting…' : 'Reset to default'}</button
				>{/if}</span
		>
	{/if}
{/snippet}

{#snippet source(f: Field)}
	<p class="small muted srcline">{@render sourceInline(f)}</p>
{/snippet}

<style>
	.lbl {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-quiet);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
	}
	.docs {
		font-size: var(--text-sm);
	}
	.docs a {
		font-family: var(--font-mono);
	}
	.notes {
		display: grid;
		gap: var(--space-3);
		max-width: 1080px;
	}
	.notes:has(*) {
		margin-bottom: var(--space-6);
	}
	code.block {
		display: block;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		margin: var(--space-2) 0;
		padding: var(--space-2) var(--space-3);
		font-size: var(--text-xs);
	}
	.layout {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(260px, 320px);
		gap: var(--space-8);
		align-items: start;
		max-width: 1080px;
	}
	.form {
		display: grid;
		gap: var(--space-8);
		min-width: 0;
	}
	fieldset {
		border: 0;
		margin: 0;
		padding: 0;
		min-width: 0;
		display: grid;
		gap: var(--space-8);
	}
	.card {
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.group {
		display: grid;
		gap: var(--space-3);
	}
	.group-head h2,
	.adv summary h2 {
		font-size: var(--text-base);
	}
	.group-head p {
		margin-top: var(--space-1);
		color: var(--fg-quiet);
		font-size: var(--text-sm);
		max-width: 64ch;
	}
	.adv summary {
		cursor: pointer;
		list-style-position: outside;
		margin-left: var(--space-4);
	}
	.adv summary h2 {
		display: inline;
	}
	.fields {
		display: grid;
		gap: var(--space-5);
		padding: var(--space-4);
	}
	.dim {
		opacity: 0.6;
	}
	.risk {
		font-size: var(--text-sm);
		line-height: 1.5;
		padding: var(--space-3);
		border-left: 3px solid var(--accent);
		background: color-mix(in oklab, var(--accent), transparent 93%);
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
		color: var(--fg-2);
	}
	.risk[data-mode='open'] {
		border-left-color: var(--warn);
		background: color-mix(in oklab, var(--warn), transparent 92%);
	}
	.risk[data-mode='off'] {
		border-left-color: var(--border-strong);
		background: var(--surface-raised);
	}
	.risk-l {
		display: block;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		color: var(--fg-quiet);
		margin-bottom: 2px;
	}
	.quota input {
		max-width: 16rem;
		font-family: var(--font-mono);
	}
	.tgl {
		display: grid;
		gap: var(--space-2);
		padding-top: var(--space-4);
		border-top: 1px solid var(--border-soft);
	}
	.src {
		display: inline;
	}
	.srcline {
		margin: 0;
	}
	.linkish {
		border: 0;
		background: none;
		padding: 0;
		font: inherit;
		color: var(--accent-text);
		text-decoration: underline;
		cursor: pointer;
	}
	.linkish:disabled {
		color: var(--fg-quiet);
		cursor: default;
	}
	input[aria-invalid='true'] {
		border-color: var(--danger);
	}
	.err {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		line-height: 1.4;
		color: var(--danger-text);
	}
	.small {
		font-size: var(--text-xs);
	}
	.side {
		position: sticky;
		top: var(--space-6);
		display: grid;
		gap: var(--space-4);
	}
	.status {
		padding: var(--space-4);
		display: grid;
		gap: var(--space-3);
	}
	.st-head {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: var(--space-2);
	}
	.st-head h2 {
		font-size: var(--text-base);
	}
	.lead {
		margin: 0;
	}
	[data-tone='ok'] .dot {
		background: var(--success);
	}
	[data-tone='warn'] .dot {
		background: var(--warn);
	}
	[data-tone='bad'] .dot {
		background: var(--danger);
	}
	[data-tone='off'] .dot {
		background: var(--fg-quiet);
	}
	.checks {
		list-style: none;
		margin: 0;
		padding: var(--space-3) 0 0;
		border-top: 1px solid var(--border-soft);
		display: grid;
		gap: var(--space-2);
		font-size: var(--text-sm);
	}
	.checks li {
		display: grid;
		grid-template-columns: 1.25rem minmax(0, 1fr);
		gap: var(--space-1);
	}
	.mark {
		font-family: var(--font-mono);
		color: var(--success-text, var(--success));
		text-align: center;
	}
	.checks li.bad .mark {
		color: var(--danger-text);
	}
	.checks li.warn .mark {
		color: var(--warn-text);
	}
	.detail {
		display: block;
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		line-height: 1.45;
		overflow-wrap: anywhere;
		margin-top: 2px;
	}
	.problem {
		color: var(--danger-text);
		font-family: var(--font-mono);
		overflow-wrap: anywhere;
	}
	dl {
		display: grid;
		grid-template-columns: max-content minmax(0, 1fr);
		gap: var(--space-2) var(--space-4);
		margin: 0;
		font-size: var(--text-sm);
	}
	dl:empty {
		display: none;
	}
	dt {
		color: var(--fg-quiet);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		align-self: center;
	}
	dd {
		margin: 0;
		font-family: var(--font-mono);
		min-width: 0;
	}
	.wrap {
		overflow-wrap: anywhere;
	}
	.test {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		align-items: baseline;
		margin: 0;
		font-size: var(--text-sm);
	}
	.test a {
		font-family: var(--font-mono);
	}
	.checked {
		padding-top: var(--space-3);
		border-top: 1px solid var(--border-soft);
		margin: 0;
	}
	.bar {
		position: sticky;
		bottom: 0;
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
		z-index: 1;
	}
	.bar.dirty {
		border-top-color: var(--accent);
	}
	.meta {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.dirty .meta {
		color: var(--accent-text);
	}
	.btns {
		display: flex;
		gap: var(--space-2);
		margin-left: auto;
	}
	.users {
		max-width: 1080px;
	}
	.toolbar {
		display: flex;
		align-items: center;
		gap: var(--space-4);
		margin-bottom: var(--space-4);
		flex-wrap: wrap;
	}
	.search {
		flex: 1 1 240px;
		max-width: 420px;
	}
	.count {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-variant-numeric: tabular-nums;
	}
	.ident {
		font-family: var(--font-mono);
		font-weight: 600;
		overflow-wrap: break-word;
	}
	.at {
		font-weight: 400;
		color: var(--fg-quiet);
	}
	tr.banned .who {
		color: var(--fg-quiet);
		text-decoration: line-through;
	}
	.badges {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-2);
		align-items: center;
		margin-top: 2px;
	}
	.badges:empty {
		display: none;
	}
	.mono {
		font-family: var(--font-mono);
	}
	.seen {
		white-space: nowrap;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}
	.acts {
		text-align: right;
		white-space: nowrap;
	}
	.acts :global(.btn) {
		margin-left: var(--space-1);
	}
	.empty {
		color: var(--fg-quiet);
		text-align: center;
		padding: var(--space-8) var(--space-4);
	}
	.pager {
		display: flex;
		justify-content: center;
		margin-top: var(--space-4);
	}
	.reason {
		display: grid;
		gap: var(--space-1);
		margin-top: var(--space-3);
		font-size: var(--text-sm);
	}
	.show-s {
		display: none;
	}
	@media (max-width: 900px) {
		.layout {
			grid-template-columns: minmax(0, 1fr);
		}
		.side {
			position: static;
			order: -1;
		}
		.hide-m {
			display: none;
		}
	}
	@media (max-width: 640px) {
		.hide-s {
			display: none;
		}
		.show-s {
			display: inline;
		}
		.acts {
			white-space: normal;
			text-align: left;
		}
		.acts :global(.btn) {
			margin: var(--space-1) var(--space-1) 0 0;
		}
	}
</style>
