<script lang="ts">
	import { onMount } from 'svelte';
	import { api, isUnsupported } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { datetime } from '$lib/format';
	import type { NetworkSettings, Registration, TextCreators, VoiceCreators } from '$lib/types';
	import ChoiceGroup, { type Choice } from '$lib/components/ChoiceGroup.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';

	let saved = $state<NetworkSettings | null>(null);
	let draft = $state<NetworkSettings | null>(null);
	let error = $state('');
	let unsupported = $state('');
	let saving = $state(false);

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
			label: 'Any registered user',
			detail: 'Signed-in people can start a channel and become its founder.'
		},
		{
			value: 'admins',
			label: 'Admins only',
			detail: 'Only network admins create channels; everyone else joins existing ones.'
		}
	];
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
			saved = (await api<{ settings: NetworkSettings }>('GET', '/settings')).settings;
			draft = { ...saved };
			unsupported = '';
		} catch (e) {
			if (isUnsupported(e)) unsupported = e.message;
			else error = errorText(e);
		}
	}
	onMount(load);

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
		return out;
	});
	let dirty = $derived(Object.keys(changes).length > 0);

	async function save(e: Event) {
		e.preventDefault();
		if (!dirty) return;
		saving = true;
		try {
			saved = (await api<{ settings: NetworkSettings }>('PATCH', '/settings', changes)).settings;
			draft = { ...saved };
			toasts.push('Network settings saved.');
		} catch (err) {
			toasts.push(errorText(err), 'bad');
		} finally {
			saving = false;
		}
	}
	function discard() {
		if (saved) draft = { ...saved };
	}
</script>

<svelte:head><title>Network settings · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Network settings</h1>
		<p>Who can join this network and who can create channels. Changes apply to new requests straight away.</p>
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
		</section>

		<section aria-labelledby="ch-h">
			<h2 id="ch-h" class="sr-only">Channels</h2>
			<ChoiceGroup
				legend="Who can create text channels"
				hint="The creator becomes the channel’s founder and can manage it."
				name="channel_creation"
				choices={text}
				bind:value={draft.channel_creation}
			/>
			<ChoiceGroup
				legend="Who can create voice rooms"
				hint="A voice room belongs to a text channel. Admins can always create them."
				name="voice_channel_creation"
				choices={voice}
				bind:value={draft.voice_channel_creation}
			/>
		</section>

		<div class="bar" class:dirty>
			<p class="meta" aria-live="polite">
				{#if dirty}
					{Object.keys(changes).length === 1
						? 'One unsaved change'
						: `${Object.keys(changes).length} unsaved changes`}
				{:else if saved?.updated_at}
					Last changed <Time at={saved.updated_at} /> by
					{#if saved.updated_by}<a href="/users/{saved.updated_by}"
							>{saved.updated_by_username || saved.updated_by}</a
						>{:else}someone{/if}
					<span class="sr-only">({datetime(saved.updated_at)})</span>
				{:else}
					Using the auth service’s defaults.
				{/if}
			</p>
			<div class="btns">
				<button class="btn btn-ghost" type="button" disabled={!dirty || saving} onclick={discard}
					>Discard</button
				>
				<button class="btn btn-primary" disabled={!dirty || saving}
					>{saving ? 'Saving…' : 'Save changes'}</button
				>
			</div>
		</div>
	</form>
{/if}

<style>
	.settings {
		display: grid;
		gap: var(--space-10);
		max-width: 760px;
	}
	section {
		display: grid;
		gap: var(--space-8);
	}
	.toggle-row {
		padding: var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
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
