<script lang="ts">
	import { beforeNavigate } from '$app/navigation';
	import { page } from '$app/state';
	import { api, isUnsupported } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { stackConfig } from '$lib/stackconfig.svelte';
	import { AUTOJOIN_KEY, channelProblem, channelsChanged, foldChannel, previewLine } from '$lib/channels';
	import type { ChannelAccess, RoleChannel } from '$lib/types';
	import Notice from './Notice.svelte';
	import State from './State.svelte';

	/**
	 * A role's channels (the Roles page's Channels tab): add and remove
	 * channels, auto-join each, see which are private, and a preview of who
	 * gets joined where. `roleId` is auth's ("everyone" for Member).
	 */
	let {
		roleId,
		roleName,
		everyone = false,
		lock = ''
	}: { roleId: string; roleName: string; everyone?: boolean; lock?: string } = $props();

	let saved = $state<RoleChannel[] | null>(null);
	let draft = $state<RoleChannel[]>([]);
	let access = $state<ChannelAccess[]>([]);
	let error = $state('');
	let unsupported = $state('');
	let saving = $state(false);
	let adding = $state('');
	let addTried = $state(false);
	let loadedFor = '';

	let changed = $derived(!!saved && channelsChanged(draft, saved));
	let addIssue = $derived.by(() => {
		if (!addTried && !adding.trim()) return '';
		const p = channelProblem(adding);
		if (p) return p;
		return draft.some((c) => c.channel === foldChannel(adding))
			? `${foldChannel(adding)} is already listed.`
			: '';
	});
	let autojoinOn = $derived(stackConfig.key(AUTOJOIN_KEY)?.value === true);
	let configLoaded = $derived(!!stackConfig.view);
	let preview = $derived(previewLine(roleName, draft, everyone, autojoinOn || !configLoaded));
	const isPrivate = (ch: string) => access.some((a) => a.channel === ch && a.private);

	async function load() {
		try {
			const [r, a] = await Promise.all([
				api<{ channels: RoleChannel[] }>('GET', `/roles/${encodeURIComponent(roleId)}/channels`),
				api<{ channels: ChannelAccess[] }>('GET', '/channel-access')
			]);
			saved = sortList(r.channels);
			draft = sortList(r.channels);
			access = a.channels;
			error = unsupported = '';
		} catch (e) {
			if (isUnsupported(e)) unsupported = e.message;
			else error = errorText(e);
		}
	}
	$effect(() => {
		if (loadedFor !== roleId) {
			loadedFor = roleId;
			saved = null;
			load();
			stackConfig.load();
		}
	});

	function sortList(l: RoleChannel[]) {
		return [...l].sort((a, b) => a.channel.localeCompare(b.channel));
	}

	function add(e?: Event) {
		e?.preventDefault();
		addTried = true;
		if (addIssue || !adding.trim()) return;
		draft = sortList([...draft, { channel: foldChannel(adding), autojoin: true }]);
		adding = '';
		addTried = false;
	}
	function remove(ch: string) {
		draft = draft.filter((c) => c.channel !== ch);
	}
	function setAutojoin(ch: string, on: boolean) {
		draft = draft.map((c) => (c.channel === ch ? { ...c, autojoin: on } : c));
	}

	async function save() {
		if (!changed) return;
		saving = true;
		try {
			const r = await api<{ channels: RoleChannel[] }>(
				'PUT',
				`/roles/${encodeURIComponent(roleId)}/channels`,
				{ channels: draft }
			);
			saved = sortList(r.channels);
			draft = sortList(r.channels);
			toasts.push(everyone ? 'Saved everyone’s channels.' : `Saved ${roleName}’s channels.`);
		} catch (err) {
			toasts.push(errorText(err), 'bad');
		} finally {
			saving = false;
		}
	}

	beforeNavigate((nav) => {
		if (changed && !saving && nav.to?.url.pathname !== page.url.pathname) {
			if (!confirm(`Discard your unsaved channel changes to ${roleName}?`)) nav.cancel();
		}
	});
</script>

{#if unsupported}
	<State kind="unsupported" title="Role channels need a newer auth service">
		<p>{unsupported}.</p>
	</State>
{:else if error}
	<State kind="error">
		{error}
		{#snippet action()}<button type="button" class="btn btn-small" onclick={load}>Try again</button>{/snippet}
	</State>
{:else if !saved}
	<State kind="loading" title="Loading channels…" />
{:else}
	<div class="rc">
		<p class="intro">
			{#if everyone}Channels every account gets.{:else}Channels that come with {roleName}.{/if}
			Private ones can only be joined by the roles that list them (and IRC operators); mark channels private on
			<a href="/channels">Channel access</a>.
		</p>

		<p
			class="preview"
			class:off={configLoaded && !autojoinOn}
			aria-live="polite"
			data-testid="channel-preview"
		>
			<span class="pv-label">Preview</span>
			<span>{preview}</span>
			{#if configLoaded && !autojoinOn}
				<span class="pv-note"
					>Auto-join is off for the network. Turn it on under <a href="/channels#autojoin-h">Channel access</a
					>.</span
				>
			{/if}
		</p>

		{#if !lock}
			<form class="add" onsubmit={add} novalidate>
				<label class="field">
					<span>Add a channel</span>
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
							aria-describedby="add-hint"
						/>
						<button class="btn" type="submit">Add</button>
					</span>
					<small id="add-hint" class:bad={!!addIssue}
						>{addIssue ||
							'A channel name starts with #. New channels are auto-joined; untick it to just list it.'}</small
					>
				</label>
			</form>
		{/if}

		{#if draft.length === 0}
			<State kind="empty" title={everyone ? 'No channels for everyone' : `No channels for ${roleName}`}>
				{#if lock}Nothing is listed.{:else}Add one above, like #staff for a team or #welcome for everyone.{/if}
			</State>
		{:else}
			<ul class="list" aria-label="Channels">
				{#each draft as c (c.channel)}
					{@const priv = isPrivate(c.channel)}
					<li>
						<span class="name">{c.channel}</span>
						<span
							class="tag {priv ? 'tag-warn' : ''}"
							title={priv
								? 'Only members of roles that list it, and IRC operators, can join.'
								: 'Anyone can join. The listing only drives auto-join.'}>{priv ? 'Private' : 'Public'}</span
						>
						<label class="aj">
							<input
								type="checkbox"
								checked={c.autojoin}
								disabled={!!lock}
								onchange={(e) => setAutojoin(c.channel, e.currentTarget.checked)}
							/>
							Auto-join
						</label>
						{#if !lock}
							<button
								type="button"
								class="btn btn-small btn-ghost"
								onclick={() => remove(c.channel)}
								aria-label="Remove {c.channel}">Remove</button
							>
						{/if}
					</li>
				{/each}
			</ul>
		{/if}

		{#if lock}
			<Notice tone="info">{lock}</Notice>
		{:else}
			<div class="bar" class:dirty={changed}>
				<p class="meta" aria-live="polite">{changed ? 'Unsaved channel changes' : 'All changes saved'}</p>
				<div class="btns">
					<button
						class="btn btn-ghost"
						type="button"
						disabled={!changed || saving}
						onclick={() => saved && (draft = sortList(saved))}>Discard</button
					>
					<button class="btn btn-primary" type="button" disabled={!changed || saving} onclick={save}
						>{saving ? 'Saving…' : 'Save channels'}</button
					>
				</div>
			</div>
		{/if}
	</div>
{/if}

<style>
	.rc {
		display: grid;
		gap: var(--space-5);
		max-width: 760px;
	}
	.intro {
		color: var(--fg-quiet);
		font-size: var(--text-sm);
		max-width: 64ch;
	}
	.preview {
		display: grid;
		gap: var(--space-1);
		padding: var(--space-3) var(--space-4);
		border: 1px solid var(--border);
		border-left: 3px solid var(--accent);
		border-radius: var(--radius-sm);
		background: var(--surface);
		font-size: var(--text-sm);
		color: var(--fg);
	}
	.preview.off {
		border-left-color: var(--border-strong);
		color: var(--fg-2);
	}
	.pv-label {
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: 600;
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		color: var(--fg-quiet);
	}
	.pv-note {
		color: var(--fg-quiet);
		font-size: var(--text-xs);
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
	.list {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.list li {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto auto auto;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-2) var(--space-2) var(--space-2) var(--space-4);
		min-height: 48px;
	}
	.list li + li {
		border-top: 1px solid var(--border-soft);
	}
	.name {
		font-family: var(--font-mono);
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.aj {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		font-size: var(--text-sm);
		cursor: pointer;
		white-space: nowrap;
	}
	.aj input {
		width: 16px;
		height: 16px;
		accent-color: var(--accent);
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
	@media (max-width: 560px) {
		.list li {
			grid-template-columns: minmax(0, 1fr) auto;
		}
		.list li .aj {
			grid-column: 1;
		}
	}
</style>
