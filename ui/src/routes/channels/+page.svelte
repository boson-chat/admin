<script lang="ts">
	import { beforeNavigate } from '$app/navigation';
	import { page } from '$app/state';
	import { api, isUnsupported } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { stackConfig } from '$lib/stackconfig.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import {
		AUTOJOIN_KEY,
		REJOIN_KEY,
		channelProblem,
		foldChannel,
		previewLine,
		type Rejoin
	} from '$lib/channels';
	import type { ChannelOverview } from '$lib/types';
	import ChoiceGroup, { type Choice } from '$lib/components/ChoiceGroup.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import RoleChip from '$lib/components/RoleChip.svelte';
	import State from '$lib/components/State.svelte';
	import Toggle from '$lib/components/Toggle.svelte';

	// ---- channel access (auth) ----
	let ov = $state<ChannelOverview | null>(null);
	let error = $state('');
	let unsupported = $state('');
	/** channel → private, as on screen (saved + unsaved). */
	let draft = $state<Record<string, boolean>>({});
	let saving = $state(false);
	let adding = $state('');
	let addTried = $state(false);

	let canEdit = $derived(session.can('roles.manage'));
	let savedMap = $derived(Object.fromEntries((ov?.channels ?? []).map((c) => [c.channel, c.private])));
	let rows = $derived.by(() => {
		const names = new Set([...Object.keys(savedMap), ...Object.keys(draft)]);
		return [...names].sort().map((name) => ({
			channel: name,
			private: draft[name] ?? savedMap[name] ?? false,
			roles: ov?.channels.find((c) => c.channel === name)?.roles ?? [],
			added: !(name in savedMap)
		}));
	});
	let accessChanged = $derived(rows.filter((r) => r.private !== (savedMap[r.channel] ?? false)).length);
	let privateCount = $derived(rows.filter((r) => r.private).length);
	let addIssue = $derived.by(() => {
		if (!addTried && !adding.trim()) return '';
		const p = channelProblem(adding);
		if (p) return p;
		const f = foldChannel(adding);
		return rows.some((r) => r.channel === f && r.private) ? `${f} is already private.` : '';
	});

	async function load() {
		try {
			ov = await api<ChannelOverview>('GET', '/channels');
			draft = {};
			error = unsupported = '';
		} catch (e) {
			if (isUnsupported(e)) unsupported = e.message;
			else error = errorText(e);
		}
	}

	function setPrivate(ch: string, on: boolean) {
		draft = { ...draft, [ch]: on };
	}
	function addPrivate(e: Event) {
		e.preventDefault();
		addTried = true;
		if (addIssue || !adding.trim()) return;
		setPrivate(foldChannel(adding), true);
		adding = '';
		addTried = false;
	}

	async function saveAccess() {
		if (!accessChanged) return;
		saving = true;
		try {
			// Public channels no role lists drop out of the list.
			const channels = rows
				.filter((r) => r.private || r.roles.length)
				.map((r) => ({ channel: r.channel, private: r.private }));
			await api('PUT', '/channel-access', { channels });
			toasts.push(
				privateCount === 1
					? 'Saved. One channel is private.'
					: `Saved. ${privateCount || 'No'} channels are private.`
			);
			await load();
		} catch (err) {
			toasts.push(errorText(err), 'bad');
		} finally {
			saving = false;
		}
	}

	// ---- auto-join switches (config file) ----
	let view = $derived(stackConfig.view);
	let autoKey = $derived(view?.keys.find((k) => k.key === AUTOJOIN_KEY));
	let rejoinKey = $derived(view?.keys.find((k) => k.key === REJOIN_KEY));
	let configOff = $derived(session.info?.features.config_file === false || view?.enabled === false);
	let settingsEditable = $derived(!!autoKey?.editable);
	let savedAuto = $derived(autoKey?.value === true);
	let savedRejoin = $derived<Rejoin>(rejoinKey?.value === 'once' ? 'once' : 'login');
	let autojoin = $state(false);
	let rejoin = $state<Rejoin>('login');
	let filledFrom = '';
	$effect(() => {
		const tag = `${view?.etag ?? ''}|${savedAuto}|${savedRejoin}`;
		if (view && tag !== filledFrom) {
			filledFrom = tag;
			autojoin = savedAuto;
			rejoin = savedRejoin;
		}
	});
	let settingsChanged = $derived(!!view && (autojoin !== savedAuto || rejoin !== savedRejoin));
	let savingSettings = $state(false);
	const rejoinChoices: Choice<Rejoin>[] = [
		{
			value: 'login',
			label: 'Join them again at next login',
			detail: 'Their roles’ auto-join channels come back every time they sign in.',
			note: 'Default'
		},
		{
			value: 'once',
			label: 'Leave them out',
			detail: 'They’re joined once, when they first get the channel. If they leave, it sticks.'
		}
	];

	async function saveSettings(e: Event) {
		e.preventDefault();
		if (!settingsChanged) return;
		savingSettings = true;
		try {
			const set: Record<string, boolean | string> = {};
			if (autojoin !== savedAuto) set[AUTOJOIN_KEY] = autojoin;
			if (rejoin !== savedRejoin) set[REJOIN_KEY] = rejoin;
			await stackConfig.save({ set });
			toasts.push('Auto-join saved. IRC picks it up within seconds.');
		} catch (err) {
			toasts.push(errorText(err), 'bad');
			if (err instanceof Error && /changed since you loaded/.test(err.message)) await stackConfig.load();
		} finally {
			savingSettings = false;
		}
	}

	// ---- preview ----
	let previews = $derived(
		(ov?.roles ?? [])
			.filter((r) => r.channels.length)
			.map((r) => ({ ...r, line: previewLine(r.name, r.channels, !!r.everyone, savedAuto || !view) }))
	);

	$effect(() => {
		load();
		stackConfig.load();
	});

	beforeNavigate((nav) => {
		if (
			(accessChanged || settingsChanged) &&
			!saving &&
			!savingSettings &&
			nav.to?.url.pathname !== page.url.pathname
		) {
			if (!confirm('Discard your unsaved changes to channel access?')) nav.cancel();
		}
	});
</script>

<svelte:head><title>Channel access · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Channel access</h1>
		<p>
			Which channels are private, and whether people are joined to their roles’ channels when they sign in.
			Give a role its channels on its <a href="/roles">Roles</a> page, under Channels.
		</p>
	</div>
</div>

<div class="grid">
	<section class="card" aria-labelledby="autojoin-h">
		<h2 id="autojoin-h">Auto-join</h2>
		{#if configOff}
			<Notice tone="info">
				These switches live in the stack config file, which this panel isn’t set up to edit (<code
					>BOSON_CONFIG</code
				>). Set <code>roles.channels.autojoin</code> and
				<code>roles.channels.rejoin</code> in <code>config/boson.yaml</code> by hand.
			</Notice>
		{:else if stackConfig.error && !view}
			<Notice tone="bad">Couldn’t read the stack config file: {stackConfig.error}</Notice>
		{:else if !view}
			<State kind="loading" title="Loading settings…" />
		{:else}
			{#if !settingsEditable}
				<Notice tone="info"
					>You can look but not change these: saving needs <code>settings.manage</code>.</Notice
				>
			{/if}
			<form class="settings" onsubmit={saveSettings}>
				<fieldset disabled={!settingsEditable || savingSettings}>
					<div class="toggle-row">
						<Toggle
							label="Auto-join members to their roles’ channels"
							detail="When someone signs in to IRC, from any client, the server joins them to every channel their roles list with auto-join ticked. Getting a role joins them straight away."
							bind:checked={autojoin}
						/>
					</div>
					<div class:dim={!autojoin}>
						<ChoiceGroup
							legend="After someone leaves a channel"
							hint="What happens at their next sign-in when they’ve left one of their auto-join channels."
							name="rejoin"
							choices={rejoinChoices}
							bind:value={rejoin}
						/>
					</div>
				</fieldset>
				<div class="row-end">
					<span class="meta" aria-live="polite"
						>{settingsChanged ? 'Unsaved changes' : 'Saved in config/boson.yaml'}</span
					>
					<button class="btn btn-primary" disabled={!settingsChanged || savingSettings || !settingsEditable}
						>{savingSettings ? 'Saving…' : 'Save auto-join'}</button
					>
				</div>
			</form>
		{/if}
	</section>

	<section class="card" aria-labelledby="preview-h">
		<h2 id="preview-h">Who gets joined where</h2>
		{#if !ov}
			<p class="muted">{unsupported ? 'Needs a newer auth service.' : 'Loading…'}</p>
		{:else if previews.length === 0}
			<p class="muted">No role lists a channel yet. Open a role and add some under Channels.</p>
		{:else}
			{#if view && !savedAuto}
				<p class="muted small">Auto-join is off, so this is what would happen with it on.</p>
			{/if}
			<ul class="previews">
				{#each previews as r (r.id)}
					<li>
						<RoleChip
							role={{ id: r.id, name: r.name, color: r.color }}
							size="small"
							href={r.everyone ? undefined : `/roles/${encodeURIComponent(r.id)}?tab=channels`}
						/>
						<span>{r.line}</span>
					</li>
				{/each}
			</ul>
		{/if}
	</section>
</div>

<section class="access" aria-labelledby="access-h">
	<div class="sec-head">
		<h2 id="access-h">Private channels</h2>
		<p>
			A private channel can only be joined by members of the roles that list it, and by IRC operators. Its
			founder and ChanServ access still work as usual. Public channels are open to anyone; listing them on a
			role only drives auto-join. To make a new private channel, create it on IRC first, then mark it private
			here.
		</p>
	</div>

	{#if unsupported}
		<State kind="unsupported" title="Channel access needs a newer auth service">
			<p>{unsupported}.</p>
		</State>
	{:else if error}
		<State kind="error">
			{error}
			{#snippet action()}<button class="btn btn-small" onclick={load}>Try again</button>{/snippet}
		</State>
	{:else if !ov}
		<State kind="loading" title="Loading channels…" />
	{:else}
		{#if canEdit}
			<form class="add" onsubmit={addPrivate} novalidate>
				<label class="field">
					<span>Make a channel private</span>
					<span class="row">
						<input
							type="text"
							bind:value={adding}
							placeholder="#staff"
							maxlength="64"
							spellcheck="false"
							autocapitalize="off"
							autocomplete="off"
							aria-invalid={!!addIssue || undefined}
							aria-describedby="addp-hint"
						/>
						<button class="btn" type="submit">Add</button>
					</span>
					<small id="addp-hint" class:bad={!!addIssue}
						>{addIssue || 'Then list it on the roles that should get in.'}</small
					>
				</label>
			</form>
		{/if}

		{#if rows.length === 0}
			<State kind="empty" title="No channels yet">
				Channels show up here once a role lists them or you make one private.
			</State>
		{:else}
			<ul class="channels" aria-label="Channels">
				{#each rows as r (r.channel)}
					<li class:priv={r.private}>
						<div class="ch">
							<span class="name">{r.channel}</span>
							<span class="tag {r.private ? 'tag-warn' : ''}">{r.private ? 'Private' : 'Public'}</span>
							{#if r.private !== (savedMap[r.channel] ?? false)}<span class="tag tag-accent">Unsaved</span
								>{/if}
						</div>
						<div class="who">
							{#if r.roles.length}
								{#each r.roles as g (g.id)}
									<span class="grant" title={g.autojoin ? 'Auto-joined' : 'Listed, not auto-joined'}>
										<RoleChip role={{ id: g.id, name: g.name, color: g.color }} size="small" />
										{#if g.autojoin}<span class="aj" aria-label="auto-join">auto</span>{/if}
									</span>
								{/each}
							{:else if r.private}
								<span class="warn small">No role lists it, so only IRC operators can join.</span>
							{:else}
								<span class="muted small">No role lists it.</span>
							{/if}
						</div>
						<div class="sw">
							<Toggle
								label="Private"
								ariaLabel="{r.channel} is private"
								checked={r.private}
								disabled={!canEdit || saving}
								onchange={(on) => setPrivate(r.channel, on)}
							/>
						</div>
					</li>
				{/each}
			</ul>
		{/if}

		{#if canEdit}
			<div class="bar" class:dirty={accessChanged > 0}>
				<p class="meta" aria-live="polite">
					{#if accessChanged}{accessChanged === 1
							? 'One unsaved change'
							: `${accessChanged} unsaved changes`}{:else}{privateCount === 1
							? 'One private channel'
							: `${privateCount || 'No'} private channels`}{/if}
				</p>
				<div class="btns">
					<button
						class="btn btn-ghost"
						type="button"
						disabled={!accessChanged || saving}
						onclick={() => (draft = {})}>Discard</button
					>
					<button
						class="btn btn-primary"
						type="button"
						disabled={!accessChanged || saving}
						onclick={saveAccess}>{saving ? 'Saving…' : 'Save channel access'}</button
					>
				</div>
			</div>
		{/if}
	{/if}
</section>

<style>
	.grid {
		display: grid;
		grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr);
		gap: var(--space-6);
		align-items: start;
		margin-bottom: var(--space-10, 48px);
	}
	.card {
		display: grid;
		gap: var(--space-4);
		padding: var(--space-5);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
		min-width: 0;
	}
	h2 {
		font-size: var(--text-lg);
	}
	.settings {
		display: grid;
		gap: var(--space-5);
	}
	fieldset {
		display: grid;
		gap: var(--space-5);
		border: 0;
		margin: 0;
		padding: 0;
		min-width: 0;
	}
	.dim {
		opacity: 0.6;
	}
	.row-end {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
	}
	.meta {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.small {
		font-size: var(--text-xs);
	}
	.warn {
		color: var(--warn-text);
	}
	.previews {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: var(--space-3);
	}
	.previews li {
		display: grid;
		gap: var(--space-1);
		justify-items: start;
		font-size: var(--text-sm);
	}
	.access {
		display: grid;
		gap: var(--space-4);
		max-width: 960px;
	}
	.sec-head {
		display: grid;
		gap: var(--space-2);
	}
	.sec-head p {
		color: var(--fg-quiet);
		font-size: var(--text-sm);
		max-width: 72ch;
	}
	.add {
		max-width: 520px;
	}
	.row {
		display: flex;
		gap: var(--space-2);
		text-transform: none;
		letter-spacing: 0;
	}
	.row input {
		flex: 1;
		min-width: 0;
		font-family: var(--font-mono);
	}
	small.bad {
		color: var(--danger-text);
	}
	input[aria-invalid='true'] {
		border-color: var(--danger);
	}
	.channels {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.channels li {
		display: grid;
		grid-template-columns: minmax(160px, 0.8fr) minmax(0, 1.6fr) auto;
		align-items: center;
		gap: var(--space-4);
		padding: var(--space-3) var(--space-4);
	}
	.channels li + li {
		border-top: 1px solid var(--border-soft);
	}
	.ch {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
	}
	.name {
		font-family: var(--font-mono);
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.who {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		min-width: 0;
	}
	.grant {
		display: inline-flex;
		align-items: center;
		gap: 4px;
	}
	.grant .aj {
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--accent-text);
	}
	.sw {
		min-width: 110px;
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
	.dirty .meta {
		color: var(--accent-text);
	}
	.btns {
		display: flex;
		gap: var(--space-2);
		margin-left: auto;
	}
	@media (max-width: 1080px) {
		.grid {
			grid-template-columns: minmax(0, 1fr);
		}
	}
	@media (max-width: 640px) {
		.channels li {
			grid-template-columns: minmax(0, 1fr) auto;
			gap: var(--space-2) var(--space-3);
		}
		.who {
			grid-column: 1 / -1;
			grid-row: 2;
		}
		.card {
			padding: var(--space-4);
		}
	}
</style>
