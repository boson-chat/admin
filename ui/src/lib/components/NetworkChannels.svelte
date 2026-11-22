<!--
	The network channels editor (irc.channels, irc.channel_founder and
	irc.autojoin_guests in the stack config file): RoleServ keeps each
	channel created and registered to the network, joins people to the
	auto-join ones and keeps restricted ones (irc.channels[].roles) to their
	roles. A controlled editor: the page holds the values and saves.
-->
<script lang="ts">
	import type { NetworkChannel, Role } from '$lib/types';
	import type { DropdownOption } from '$lib/dropdown';
	import {
		MAX_NETWORK_CHANNELS,
		MAX_TOPIC,
		channelAccessLabel,
		cleanChannelRoles,
		founderProblem,
		matchChannelRole,
		networkChannelProblem,
		topicProblem
	} from '$lib/channels';
	import Dropdown from './Dropdown.svelte';
	import Toggle from './Toggle.svelte';

	let {
		channels = $bindable([]),
		founder = $bindable(''),
		autojoinGuests = $bindable(true),
		disabled = false,
		adminsOnly = false,
		guestsAllowed = true,
		roles = []
	}: {
		channels?: NetworkChannel[];
		founder?: string;
		autojoinGuests?: boolean;
		disabled?: boolean;
		/** Channel creation is "Only admins". */
		adminsOnly?: boolean;
		/** Guest access is on. */
		guestsAllowed?: boolean;
		/** The network's roles, most senior first (for "Who can join"). */
		roles?: Role[];
	} = $props();

	const uid = $props.id();
	let adding = $state('');
	let addTried = $state(false);
	let addIssue = $derived.by(() => {
		if (!addTried && !adding.trim()) return '';
		if (channels.length >= MAX_NETWORK_CHANNELS) return `At most ${MAX_NETWORK_CHANNELS} network channels.`;
		return networkChannelProblem(
			adding,
			channels.map((c) => c.name)
		);
	});
	let founderIssue = $derived(founderProblem(founder));
	let autojoined = $derived(channels.filter((c) => c.autojoin).length);
	let restricted = $derived(channels.filter((c) => cleanChannelRoles(c.roles).length > 0));

	// "Who can join": the roles, most senior first; Member is every account.
	let roleOptions = $derived<DropdownOption[]>(
		roles.map((r) => ({
			value: r.name,
			label: r.name,
			description:
				r.builtin === 'member' ? 'Any signed-in account, no guests' : r.irc?.oper ? 'IRC oper' : undefined
		}))
	);
	/** A row's options: the roles, plus entries in the file that name no role. */
	function optionsFor(c: NetworkChannel): DropdownOption[] {
		const unknown = cleanChannelRoles(c.roles).filter((e) => !matchChannelRole(e, roles));
		return [
			...roleOptions,
			...unknown.map((e) => ({ value: e, label: e, description: 'No such role: lets nobody in' }))
		];
	}
	/** A row's chosen roles, as the options name them. */
	function chosenFor(c: NetworkChannel): string[] {
		return cleanChannelRoles(c.roles).map((e) => matchChannelRole(e, roles)?.name ?? e);
	}
	function setRoles(i: number, c: NetworkChannel, picked: string[]) {
		// in the list's order (most senior first), unknown entries last
		const order = optionsFor(c).map((o) => o.value);
		const next = [...picked].sort((a, b) => order.indexOf(a) - order.indexOf(b));
		update(i, { roles: next.length ? next : undefined });
	}
	let restrictedText = $derived(
		restricted.length === 0
			? ''
			: restricted.length > 3
				? `${restricted.length} restricted to some roles`
				: `${list(restricted.map((c) => c.name))} restricted to ${
						restricted.length === 1 ? channelAccessLabel(restricted[0].roles, roles) : 'some roles'
					}`
	);
	function list(names: string[]) {
		if (names.length <= 1) return names.join('');
		return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
	}

	function add() {
		addTried = true;
		if (addIssue || !adding.trim()) return;
		channels = [...channels, { name: adding.trim(), autojoin: channels.length === 0 }];
		adding = '';
		addTried = false;
	}
	function onAddKey(e: KeyboardEvent) {
		// Enter adds the channel instead of submitting the settings form.
		if (e.key === 'Enter') {
			e.preventDefault();
			add();
		}
	}
	function update(i: number, patch: Partial<NetworkChannel>) {
		channels = channels.map((c, j) => (j === i ? { ...c, ...patch } : c));
	}
	function remove(i: number) {
		channels = channels.filter((_, j) => j !== i);
	}
</script>

<fieldset class="nc" {disabled} aria-describedby="{uid}-hint">
	<legend>Network channels</legend>
	<p class="hint" id="{uid}-hint">
		Network channels always exist and belong to the network (founder: {#if founder.trim()}<strong
				>{founder.trim()}</strong
			>{:else}the network owner{/if}). Auto-join: new accounts join on their first login, guests when they
		connect; leaving sticks. “Who can join” keeps a channel to some roles: anyone else is refused (and
		removed), guests never get in, and the founder and IRC operators always can.
	</p>
	{#if adminsOnly}
		<p class="hint policy" data-testid="admins-only-note">
			With “Only admins”, members can only join these, role channels and channels that already exist.
		</p>
	{/if}

	<div class="card">
		<div class="row add">
			<label class="field" for="{uid}-add">
				<span>Add a channel</span>
			</label>
			<div class="add-row">
				<input
					id="{uid}-add"
					type="text"
					bind:value={adding}
					onkeydown={onAddKey}
					placeholder="#lobby"
					maxlength="64"
					spellcheck="false"
					autocapitalize="off"
					autocomplete="off"
					aria-invalid={!!addIssue || undefined}
					aria-describedby="{uid}-add-hint"
				/>
				<button class="btn" type="button" onclick={add}>Add</button>
			</div>
			<small id="{uid}-add-hint" class:bad={!!addIssue}
				>{addIssue || 'A # and then letters, digits or ASCII punctuation, like #lobby or #help.'}</small
			>
		</div>

		{#if channels.length === 0}
			<div class="row empty" data-testid="network-channels-empty">
				<span class="label">No network channels yet</span>
				<span class="detail"
					>Add one like #lobby and tick Auto-join, so new people land somewhere instead of an empty client.</span
				>
			</div>
		{:else}
			<ul class="list" aria-label="Network channels">
				{#each channels as c, i (c.name)}
					{@const tp = topicProblem(c.topic ?? '')}
					<li>
						<span class="name">{c.name}</span>
						<label class="topic">
							<span class="sr-only">Topic of {c.name}</span>
							<input
								type="text"
								value={c.topic ?? ''}
								oninput={(e) => update(i, { topic: e.currentTarget.value })}
								placeholder="Topic (optional)"
								maxlength={MAX_TOPIC + 20}
								aria-invalid={!!tp || undefined}
							/>
							{#if tp}<small class="bad">{tp}</small>{/if}
						</label>
						<div class="who">
							<span class="sr-only" id="{uid}-who-{i}">Who can join {c.name}</span>
							<Dropdown
								multiple
								align="end"
								options={optionsFor(c)}
								values={chosenFor(c)}
								onchangemany={(v) => setRoles(i, c, v)}
								labelledby="{uid}-who-{i}"
								placeholder="Everyone"
								title="Who can join {c.name}: {channelAccessLabel(c.roles, roles)}"
								summary={(o) =>
									channelAccessLabel(
										o.map((x) => x.value),
										roles
									)}
								class={cleanChannelRoles(c.roles).length ? 'restricted' : ''}
							/>
						</div>
						<label class="aj">
							<input
								type="checkbox"
								checked={c.autojoin}
								onchange={(e) => update(i, { autojoin: e.currentTarget.checked })}
								aria-label="Auto-join {c.name}"
							/>
							<span aria-hidden="true">Auto-join</span>
						</label>
						<button
							type="button"
							class="btn btn-small btn-ghost"
							onclick={() => remove(i)}
							aria-label="Remove {c.name}">Remove</button
						>
					</li>
				{/each}
			</ul>
			<p class="row count" aria-live="polite">
				{channels.length === 1 ? 'One channel' : `${channels.length} channels`}, {autojoined === 0
					? 'none auto-joined'
					: autojoined === channels.length
						? channels.length === 1
							? 'auto-joined'
							: 'all auto-joined'
						: `${autojoined} auto-joined`}{#if restrictedText}, <span data-testid="restricted-summary"
						>{restrictedText}</span
					>{/if}. A topic is set when RoleServ creates the channel and when you change it here.
			</p>
		{/if}

		<div class="row founder">
			<div class="txt">
				<label class="label" for="{uid}-founder">Founder account</label>
				<span class="detail" id="{uid}-founder-detail"
					>The account that founds network channels. Empty: the network owner (the first Owner), else
					RoleServ.</span
				>
			</div>
			<div class="ctl">
				<input
					id="{uid}-founder"
					type="text"
					bind:value={founder}
					placeholder="the network owner"
					maxlength="64"
					spellcheck="false"
					autocapitalize="off"
					autocomplete="off"
					aria-describedby="{uid}-founder-detail"
					aria-invalid={!!founderIssue || undefined}
				/>
				{#if founderIssue}<small class="bad">{founderIssue}</small>{/if}
			</div>
		</div>
		<div class="row">
			<Toggle
				label="Also auto-join guests"
				detail="People without an account are joined to the auto-join channels when they connect."
				bind:checked={autojoinGuests}
			/>
			{#if !guestsAllowed}
				<p class="note">Guest access is off, so this only matters once guests are allowed again.</p>
			{/if}
		</div>
	</div>
</fieldset>

<style>
	fieldset {
		border: 0;
		margin: 0;
		padding: 0;
		min-width: 0;
	}
	legend {
		padding: 0;
		font-family: var(--font-display);
		font-weight: 600;
		font-size: var(--text-base);
		color: var(--fg);
	}
	.hint {
		margin-top: var(--space-1);
		color: var(--fg-quiet);
		font-size: var(--text-sm);
		max-width: 64ch;
	}
	.hint strong {
		font-family: var(--font-mono);
		font-weight: 600;
		color: var(--fg-2);
	}
	.policy {
		padding-left: var(--space-3);
		border-left: 2px solid var(--accent);
		color: var(--fg-2);
	}
	.card {
		display: grid;
		margin-top: var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.row {
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}
	.row:last-child {
		border-bottom: 0;
	}
	fieldset:disabled .card {
		opacity: 0.6;
	}
	.add {
		display: grid;
		gap: var(--space-1);
	}
	.add .field span {
		font-family: var(--font-mono);
		font-weight: 600;
		font-size: var(--text-sm);
		color: var(--fg);
	}
	.add-row {
		display: flex;
		gap: var(--space-2);
		max-width: 420px;
	}
	.add-row input {
		flex: 1;
		min-width: 0;
		font-family: var(--font-mono);
	}
	small {
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	small.bad {
		color: var(--danger-text);
	}
	input[aria-invalid='true'] {
		border-color: var(--danger);
	}
	.empty {
		display: grid;
		gap: 2px;
	}
	.label {
		font-family: var(--font-mono);
		font-weight: 600;
		font-size: var(--text-sm);
		color: var(--fg);
	}
	.detail {
		font-size: var(--text-sm);
		color: var(--fg-2);
		line-height: 1.5;
	}
	.list {
		list-style: none;
		margin: 0;
		padding: 0;
		border-bottom: 1px solid var(--border-soft);
	}
	.list li {
		display: grid;
		grid-template-columns: minmax(7rem, 0.7fr) minmax(0, 2fr) 11rem auto auto;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-2) var(--space-2) var(--space-2) var(--space-4);
		min-height: 52px;
	}
	.list li + li {
		border-top: 1px solid var(--border-soft);
	}
	.name {
		font-family: var(--font-mono);
		font-weight: 600;
		overflow-wrap: anywhere;
	}
	.topic {
		display: grid;
		gap: 2px;
		min-width: 0;
	}
	.topic input {
		width: 100%;
		min-width: 0;
	}
	.who {
		min-width: 0;
	}
	.who :global(.dd) {
		width: 100%;
	}
	/* a restricted channel reads at a glance: accent hairline and tint */
	.who :global(.restricted .trigger) {
		border-color: color-mix(in oklab, var(--accent), transparent 45%);
		background: color-mix(in oklab, var(--accent), var(--surface) 92%);
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
	.count {
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		line-height: 1.5;
	}
	.founder {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 13rem;
		gap: var(--space-2) var(--space-4);
		align-items: start;
	}
	.txt {
		display: grid;
		gap: 2px;
	}
	.ctl {
		display: grid;
		gap: var(--space-1);
	}
	.ctl input {
		font-family: var(--font-mono);
	}
	.note {
		margin-top: var(--space-2);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	@media (max-width: 640px) {
		/* name, auto-join and remove; then the topic; then who can join */
		.list li {
			grid-template-columns: minmax(0, 1fr) auto auto;
			gap: var(--space-2) var(--space-3);
			padding: var(--space-3) var(--space-3) var(--space-3) var(--space-4);
		}
		.topic {
			grid-column: 1 / -1;
			grid-row: 2;
		}
		.who {
			grid-column: 1 / -1;
			grid-row: 3;
		}
		.founder {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
