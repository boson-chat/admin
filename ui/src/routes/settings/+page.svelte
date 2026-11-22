<script lang="ts">
	import { onMount } from 'svelte';
	import { api, isUnsupported } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { datetime } from '$lib/format';
	import type {
		NetworkChannel,
		NetworkSettings,
		Registration,
		TextCreators,
		VoiceCreators
	} from '$lib/types';
	import { session } from '$lib/session.svelte';
	import { stackConfig } from '$lib/stackconfig.svelte';
	import { rolesState } from '$lib/roles.svelte';
	import {
		AUTOJOIN_GUESTS_KEY,
		CHANNEL_FOUNDER_KEY,
		NETWORK_CHANNELS_KEY,
		cleanNetworkChannels,
		founderProblem,
		networkChannelsChanged,
		topicProblem
	} from '$lib/channels';
	import NetworkChannels from '$lib/components/NetworkChannels.svelte';
	import ChoiceGroup, { type Choice } from '$lib/components/ChoiceGroup.svelte';
	import Dropdown from '$lib/components/Dropdown.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';
	import {
		MAX_EXPIRY_DAYS,
		customDaysError,
		expiryChoice,
		expiryDays,
		expiryOptions,
		type ExpiryChoice
	} from '$lib/expiry';

	let saved = $state<NetworkSettings | null>(null);
	let draft = $state<NetworkSettings | null>(null);
	let error = $state('');
	let unsupported = $state('');
	let saving = $state(false);

	// With the stack config file, these settings are saved to its auth
	// section; each one set there can go back to the default.
	type FileInfo = { managed: boolean; sources: Record<string, 'file' | 'default'> };
	let file = $state<FileInfo | null>(null);
	let resetting = $state('');
	const names: Record<string, string> = {
		registration: 'Registration',
		require_email_verification: 'Verified email',
		guest_access: 'Guest access',
		guest_voice: 'Guest voice & video',
		channel_creation: 'Who can create channels',
		voice_channel_creation: 'Voice room creation',
		channel_auto_register: 'Automatic channel registration',
		channel_expiry_days: 'Channel expiry'
	};
	let inFile = $derived(file ? Object.keys(names).filter((k) => file?.sources[k] === 'file') : []);
	async function resetKey(k: string) {
		resetting = k;
		try {
			await api('PATCH', '/config', { unset: ['auth.' + k] });
			toasts.push(`${names[k]} is back to its default.`);
			await load();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			resetting = '';
		}
	}

	// Channel lifecycle (auth ≥ 1.7). Older auth doesn't send the fields:
	// the controls are disabled and never sent.
	let autoRegister = $state(true);
	let expiry = $state<ExpiryChoice>('0');
	let customDays = $state<number | null>(60);
	let customInput = $state<HTMLInputElement>();
	let autoSupported = $derived(typeof saved?.channel_auto_register === 'boolean');
	let expirySupported = $derived(typeof saved?.channel_expiry_days === 'number');
	let lifecycleSupported = $derived(autoSupported || expirySupported);
	let customError = $derived(expirySupported && expiry === 'custom' ? customDaysError(customDays) : '');

	// Guest access (auth ≥ 1.8). Older auth doesn't send it: IRC follows its
	// own default (guests allowed unless IRC_REQUIRE_SASL), and the toggle is
	// disabled and never sent.
	let guestAccess = $state(true);
	let guestSupported = $derived(typeof saved?.guest_access === 'boolean');
	// Guest voice & video (auth ≥ 1.18, default off). Only means something
	// while guest access is on; older auth doesn't send it and it's never sent.
	let guestVoice = $state(false);
	let guestVoiceSupported = $derived(typeof saved?.guest_voice === 'boolean');
	let guestsAllowed = $derived(!guestSupported || guestAccess);

	function reset(s: NetworkSettings) {
		draft = { ...s };
		guestAccess = s.guest_access ?? true;
		guestVoice = s.guest_voice ?? false;
		autoRegister = s.channel_auto_register ?? true;
		const days = s.channel_expiry_days ?? 0;
		expiry = expiryChoice(days);
		customDays = expiry === 'custom' ? days : 60;
	}

	const registration: Choice<Registration>[] = [
		{
			value: 'open',
			label: 'Open',
			detail: 'Anyone who finds the network can create an account.'
		},
		{
			value: 'invite',
			label: 'Invite only',
			detail: 'People need an invite code from the Invites page to register.'
		},
		{
			value: 'closed',
			label: 'Closed',
			detail:
				'Nobody can register. Admins can still create accounts, and sign-in providers can only sign in existing users.'
		}
	];
	const text: Choice<TextCreators>[] = [
		{
			value: 'registered',
			label: 'Anyone signed in',
			detail: 'Signed-in people start a channel by joining it, and become its founder.'
		},
		{
			value: 'admins',
			label: 'Only admins',
			detail:
				'Members can only join network channels, role channels and channels that already exist. Admins are roles with the irc.chanreg permission (Owner and Admin by default).'
		}
	];

	// Network channels (irc.* in the stack config file, saved with the rest).
	let cfg = $derived(stackConfig.view);
	let netKey = $derived(cfg?.keys?.find((k) => k.key === NETWORK_CHANNELS_KEY));
	let founderKey = $derived(cfg?.keys?.find((k) => k.key === CHANNEL_FOUNDER_KEY));
	let guestsKey = $derived(cfg?.keys?.find((k) => k.key === AUTOJOIN_GUESTS_KEY));
	let netOff = $derived(session.info?.features.config_file === false || cfg?.enabled === false);
	let netEditable = $derived(!!netKey?.editable && !!founderKey?.editable && !!guestsKey?.editable);
	let savedNet = $derived(cleanNetworkChannels((netKey?.value as NetworkChannel[] | undefined) ?? []));
	let savedFounder = $derived(typeof founderKey?.value === 'string' ? founderKey.value : '');
	let savedGuests = $derived(guestsKey?.value !== false);
	let netChannels = $state<NetworkChannel[]>([]);
	let founder = $state('');
	let autojoinGuests = $state(true);
	let netFilledFrom = '';
	function resetNet() {
		netChannels = savedNet.map((c) => ({ ...c }));
		founder = savedFounder;
		autojoinGuests = savedGuests;
	}
	$effect(() => {
		const tag = `${cfg?.etag ?? ''}|${JSON.stringify(savedNet)}|${savedFounder}|${savedGuests}`;
		if (cfg && tag !== netFilledFrom) {
			netFilledFrom = tag;
			resetNet();
		}
	});
	let netIssue = $derived(
		founderProblem(founder) || netChannels.map((c) => topicProblem(c.topic ?? '')).find(Boolean) || ''
	);
	let netChanges = $derived.by(() => {
		const set: Record<string, NetworkChannel[] | string | boolean> = {};
		const unset: string[] = [];
		if (!cfg || !netEditable) return { set, unset };
		if (networkChannelsChanged(netChannels, savedNet))
			set[NETWORK_CHANNELS_KEY] = cleanNetworkChannels(netChannels);
		const f = founder.trim();
		if (f !== savedFounder) {
			if (f) set[CHANNEL_FOUNDER_KEY] = f;
			else if (founderKey?.set) unset.push(CHANNEL_FOUNDER_KEY);
		}
		if (autojoinGuests !== savedGuests) set[AUTOJOIN_GUESTS_KEY] = autojoinGuests;
		return { set, unset };
	});
	let netChangeCount = $derived(Object.keys(netChanges.set).length + netChanges.unset.length);
	const voice: Choice<VoiceCreators>[] = [
		{
			value: 'admins',
			label: 'Admins only',
			detail: 'Voice rooms exist only where an admin made one. The tightest option for a small server.'
		},
		{
			value: 'founders',
			label: 'Channel founders',
			detail: 'Whoever founded a text channel can add a voice room to it. Admins can too.'
		},
		{
			value: 'registered',
			label: 'Any registered user',
			detail: 'Anyone signed in can open a voice room in a channel they’re in. Voice uses server bandwidth.'
		}
	];

	async function load() {
		error = '';
		try {
			const r = await api<{ settings: NetworkSettings; config?: FileInfo }>('GET', '/settings');
			saved = r.settings;
			file = r.config ?? null;
			reset(saved);
			unsupported = '';
		} catch (e) {
			if (isUnsupported(e)) unsupported = e.message;
			else error = errorText(e);
		}
	}
	onMount(() => {
		load();
		stackConfig.load();
		// the roles a network channel can be kept to (Who can join)
		if (!rolesState.loaded) rolesState.load();
	});

	const keys = [
		'registration',
		'require_email_verification',
		'channel_creation',
		'voice_channel_creation'
	] as const;
	let changes = $derived.by(() => {
		if (!saved || !draft) return {};
		const out: Partial<NetworkSettings> = {};
		for (const k of keys) if (draft[k] !== saved[k]) Object.assign(out, { [k]: draft[k] });
		if (autoSupported && autoRegister !== saved.channel_auto_register)
			out.channel_auto_register = autoRegister;
		if (guestSupported && guestAccess !== saved.guest_access) out.guest_access = guestAccess;
		if (guestVoiceSupported && guestVoice !== saved.guest_voice) out.guest_voice = guestVoice;
		const days = expiryDays(expiry, customDays);
		if (expirySupported && days !== null && days !== saved.channel_expiry_days)
			out.channel_expiry_days = days;
		return out;
	});
	let changeCount = $derived(Object.keys(changes).length + netChangeCount);
	// An invalid custom expiry is an unsaved edit too (Discard undoes it).
	let dirty = $derived(changeCount > 0 || !!customError || !!netIssue);
	let blocker = $derived(
		customError ? 'Fix the expiry to save' : netIssue ? 'Fix the network channels to save' : ''
	);

	async function save(e: Event) {
		e.preventDefault();
		if (!dirty || blocker) return;
		saving = true;
		try {
			// The network channels first: the If-Match is the file on screen.
			if (netChangeCount) await stackConfig.save(netChanges);
			if (Object.keys(changes).length) {
				const r = await api<{ settings: NetworkSettings; config?: FileInfo }>('PATCH', '/settings', changes);
				saved = r.settings;
				file = r.config ?? null;
				reset(saved);
				if (netChangeCount) await stackConfig.load();
			}
			toasts.push('Network settings saved.');
		} catch (err) {
			toasts.push(errorText(err), 'bad');
			if (err instanceof Error && /changed since you loaded/.test(err.message)) await stackConfig.load();
		} finally {
			saving = false;
		}
	}
	function discard() {
		if (saved) reset(saved);
		resetNet();
	}
</script>

<svelte:head><title>Network settings · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Network settings</h1>
		<p>
			Who can join this network, who can create channels and which channels it always has. Changes apply to
			new requests straight away.
		</p>
	</div>
</div>

{#if unsupported}
	<State kind="unsupported" title="Network settings need a newer auth service">
		<p>{unsupported}.</p>
		<p>
			Until then these settings come from the auth service’s environment (<code>REGISTRATION</code>,
			<code>REQUIRE_EMAIL_VERIFICATION</code>) and need a restart to change.
		</p>
	</State>
{:else if error}
	<State kind="error">
		{error}
		{#snippet action()}<button class="btn btn-small" onclick={load}>Try again</button>{/snippet}
	</State>
{:else if !draft}
	<State kind="loading" title="Loading settings…" />
{:else}
	{#if file?.managed}
		<div class="file-note">
			<Notice tone="info">
				Saved to the stack config file (<code>auth:</code> in <a href="/config">config/boson.yaml</a>). The
				auth service picks changes up within seconds.
				{#if inFile.length}
					Set in the file, so they win over the environment:
					<span class="in-file">
						{#each inFile as k (k)}
							<span class="pair"
								>{names[k]}
								<button
									class="btn btn-small btn-ghost"
									type="button"
									disabled={resetting === k}
									onclick={() => resetKey(k)}
									aria-label="Reset {names[k]} to its default"
									>{resetting === k ? 'Resetting…' : 'Reset'}</button
								></span
							>
						{/each}
					</span>
				{:else}
					Everything here is at its default.
				{/if}
			</Notice>
		</div>
	{/if}
	<form class="settings" onsubmit={save}>
		<section aria-labelledby="join-h">
			<h2 id="join-h" class="sr-only">Joining</h2>
			<ChoiceGroup
				legend="Registration"
				hint="How new accounts are created. Existing accounts keep working whatever you pick."
				name="registration"
				choices={registration}
				bind:value={draft.registration}
			/>
			<div class="toggle-row">
				<Toggle
					label="Require a verified email"
					detail="New accounts must click the link in their verification email before they can sign in."
					bind:checked={draft.require_email_verification}
				/>
			</div>
			<div class="guest" class:off={guestSupported && !guestAccess}>
				<div class="toggle-row">
					<Toggle
						label="Guest access: let people join without an account"
						detail="Anyone can connect to IRC with just a nickname. Guests can chat in channels, but can’t register channels or have roles, and join voice rooms only if allowed below. Turn this off to require everyone to sign in."
						disabled={!guestSupported}
						bind:checked={guestAccess}
					/>
				</div>
				{#if !guestSupported}
					<Notice tone="info">
						Requires auth 1.8 or newer. Until then IRC follows the stack’s
						<code>IRC_REQUIRE_SASL</code> setting, which allows guests unless it’s set.
					</Notice>
				{:else if !guestAccess}
					<p class="hint" aria-live="polite">
						{#if saved?.guest_access}
							IRC will refuse connections that don’t sign in, within about 30 seconds of saving. Guests
							already connected stay until they leave.
						{:else}
							IRC refuses connections that don’t sign in.
						{/if}
						People with an account sign in as usual from the app, web and IRC clients.
					</p>
				{/if}
			</div>
			<div class="guest">
				<div class="toggle-row">
					<Toggle
						label="Guests can join voice & video"
						detail="Guests can talk and share video in voice rooms open to everyone, under their nickname with a guest badge. Nicknames that belong to an account are refused, and rooms limited to channel members or roles stay closed to them. Voice uses server bandwidth."
						disabled={!guestVoiceSupported || !guestsAllowed}
						bind:checked={guestVoice}
					/>
				</div>
				{#if !guestVoiceSupported}
					<Notice tone="info">
						Requires auth 1.18 or newer. Until then guests can’t use voice or video.
					</Notice>
				{:else if !guestsAllowed}
					<p class="hint" aria-live="polite">
						Turn on guest access first: with it off nobody joins as a guest.
						{#if guestVoice}This stays on for when guests are allowed again.{/if}
					</p>
				{/if}
			</div>
		</section>

		<section aria-labelledby="ch-h">
			<h2 id="ch-h" class="sr-only">Channels</h2>
			<ChoiceGroup
				legend="Who can create channels"
				hint="Creating a text channel on IRC: whoever creates it becomes its founder and can manage it."
				name="channel_creation"
				choices={text}
				bind:value={draft.channel_creation}
			/>
			{#if netOff}
				<div class="net-off">
					<h3>Network channels</h3>
					<Notice tone="info">
						Network channels live in the stack config file, which this panel isn’t set up to edit (<code
							>BOSON_CONFIG</code
						>). Set <code>irc.channels</code> in <code>config/boson.yaml</code> by hand.
					</Notice>
				</div>
			{:else if !cfg}
				<State kind="loading" title="Loading network channels…" />
			{:else}
				<div class="net">
					<NetworkChannels
						bind:channels={netChannels}
						bind:founder
						bind:autojoinGuests
						disabled={!netEditable || saving}
						adminsOnly={draft.channel_creation === 'admins'}
						{guestsAllowed}
						roles={rolesState.roles}
					/>
					{#if !netEditable}
						<p class="hint">You can look but not change these: saving needs <code>settings.manage</code>.</p>
					{/if}
				</div>
			{/if}
			<ChoiceGroup
				legend="Who can create voice rooms"
				hint="A voice room belongs to a text channel. Admins can always create them."
				name="voice_channel_creation"
				choices={voice}
				bind:value={draft.voice_channel_creation}
			/>

			<fieldset class="lifecycle" disabled={!lifecycleSupported} aria-describedby="life-hint">
				<legend>Channel registration</legend>
				<p class="hint" id="life-hint">
					What happens to channels after they’re created. Changes reach IRC within about 30 seconds.
				</p>
				{#if !lifecycleSupported}
					<div class="note">
						<Notice tone="info">
							Requires auth 1.7 or newer. The auth service running here doesn’t have these settings yet, so
							they stay as they are.
						</Notice>
					</div>
				{/if}
				<div class="card">
					<div class="row">
						<Toggle
							label="Register new channels automatically"
							detail="When a signed-in user creates a channel by joining it, ChanServ registers it to their account and they become its founder. Follows who can create text channels and the limit of 15 channels per account; guests never get one."
							disabled={!autoSupported}
							bind:checked={autoRegister}
						/>
					</div>
					<div class="row expiry">
						<div class="txt">
							<span class="label" id="exp-label">Expire inactive channels</span>
							<span class="detail" id="exp-detail">
								A registered channel with no joins or messages for this long is unregistered. It stays open as
								an ordinary channel but loses its topic, modes, bans and persistent roles. Founders get a
								notice on IRC 7 days before.
							</span>
						</div>
						<div class="ctl">
							<Dropdown
								options={expiryOptions}
								bind:value={expiry}
								labelledby="exp-label"
								describedby="exp-detail"
								disabled={!expirySupported}
								align="end"
								onchange={(v) => {
									if (v === 'custom') setTimeout(() => customInput?.select());
								}}
							/>
							{#if expiry === 'custom'}
								<label class="days">
									<span class="sr-only">Expire after this many days</span>
									<input
										bind:this={customInput}
										type="number"
										inputmode="numeric"
										min="1"
										max={MAX_EXPIRY_DAYS}
										step="1"
										bind:value={customDays}
										disabled={!expirySupported}
										aria-invalid={!!customError}
										aria-describedby={customError ? 'exp-error' : undefined}
									/>
									<span class="unit" aria-hidden="true">days</span>
								</label>
							{/if}
							{#if customError}
								<p class="err" id="exp-error" role="alert">{customError}</p>
							{/if}
						</div>
						<p class="foot">
							IRC operators can exempt a channel with <code>/msg ChanServ SET #channel EXPIRY OFF</code>.
						</p>
					</div>
				</div>
			</fieldset>
		</section>

		<div class="bar" class:dirty>
			<p class="meta" aria-live="polite">
				{#if blocker}
					{blocker}
				{:else if dirty}
					{changeCount === 1 ? 'One unsaved change' : `${changeCount} unsaved changes`}
				{:else if saved?.updated_at}
					Last changed <Time at={saved.updated_at} /> by
					{#if saved.updated_by}<a href="/users/{saved.updated_by}"
							>{saved.updated_by_username || saved.updated_by}</a
						>{:else}someone{/if}
					<span class="sr-only">({datetime(saved.updated_at)})</span>
				{:else if file?.managed}
					{inFile.length ? 'Saved in the stack config file.' : 'Using the defaults.'}
				{:else}
					Using the auth service’s defaults.
				{/if}
			</p>
			<div class="btns">
				<button class="btn btn-ghost" type="button" disabled={!dirty || saving} onclick={discard}
					>Discard</button
				>
				<button class="btn btn-primary" disabled={!dirty || !!blocker || saving}
					>{saving ? 'Saving…' : 'Save changes'}</button
				>
			</div>
		</div>
	</form>
{/if}

<style>
	.file-note {
		max-width: 760px;
		margin-bottom: var(--space-8);
	}
	.in-file {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		margin-top: var(--space-2);
	}
	.pair {
		display: inline-flex;
		gap: var(--space-1);
		align-items: center;
		color: var(--fg);
	}
	.settings {
		display: grid;
		gap: var(--space-10);
		max-width: 760px;
	}
	section {
		display: grid;
		gap: var(--space-8);
	}
	.net-off {
		display: grid;
		gap: var(--space-3);
	}
	.net-off h3 {
		font-size: var(--text-base);
	}
	.guest {
		display: grid;
		gap: var(--space-2);
	}
	.toggle-row {
		padding: var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	/* the channel registration group: ChoiceGroup's legend and hint over one card of rows */
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
	.note {
		margin-top: var(--space-3);
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
	fieldset:disabled .row {
		opacity: 0.6;
	}
	fieldset:disabled .row :global(.toggle) {
		opacity: 1; /* the row is already dimmed */
	}
	.expiry {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 13rem;
		gap: var(--space-2) var(--space-4);
		align-items: start;
	}
	.txt {
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
	.ctl {
		display: grid;
		gap: var(--space-2);
	}
	.days {
		position: relative;
		display: block;
	}
	.days input {
		padding-right: 3.5rem;
	}
	.unit {
		position: absolute;
		right: var(--space-3);
		top: 50%;
		transform: translateY(-50%);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--fg-quiet);
		pointer-events: none;
	}
	.days input[aria-invalid='true'] {
		border-color: var(--danger);
	}
	.err {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		line-height: 1.4;
		color: var(--danger-text);
	}
	.foot {
		grid-column: 1 / -1;
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		line-height: 1.5;
	}
	.foot code {
		font-size: var(--text-xs);
		white-space: nowrap;
	}
	@media (max-width: 640px) {
		.expiry {
			grid-template-columns: minmax(0, 1fr);
		}
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
</style>
