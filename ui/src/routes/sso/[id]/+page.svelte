<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, isUnsupported } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { presetFor, urlLabels, type UrlRef } from '$lib/sso';
	import type { SSOProvider } from '$lib/types';
	import Confirm from '$lib/components/Confirm.svelte';
	import CopyField from '$lib/components/CopyField.svelte';
	import ProviderForm from '$lib/components/ProviderForm.svelte';
	import SetupGuide from '$lib/components/SetupGuide.svelte';
	import State from '$lib/components/State.svelte';

	let id = $derived(page.params.id ?? '');
	let provider = $state<SSOProvider | null>(null);
	let error = $state('');
	let unsupported = $state('');
	let testing = $state(false);
	let result = $state<{ ok: boolean; details: string } | null>(null);
	let formKey = $state(0);

	async function load() {
		try {
			provider = (await api<{ provider: SSOProvider }>('GET', `/sso/providers/${encodeURIComponent(id)}`))
				.provider;
			error = '';
		} catch (e) {
			if (isUnsupported(e)) unsupported = e.message;
			else error = errorText(e);
		}
	}
	$effect(() => {
		void id;
		provider = null;
		result = null;
		load();
	});

	let preset = $derived(
		provider
			? presetFor(
					provider.kind,
					provider.config.oidc?.issuer ?? provider.config.saml?.idp_metadata_url ?? '',
					provider.slug
				)
			: null
	);
	let urls = $derived.by(() => {
		if (!provider) return [] as UrlRef[];
		const keys: UrlRef[] =
			provider.kind === 'oidc' ? ['callback_url'] : ['acs_url', 'sp_entity_id', 'sp_metadata_url'];
		return keys.filter((k) => provider?.[k]);
	});

	async function test() {
		if (!provider) return;
		testing = true;
		try {
			const r = await api<{ ok: boolean; details?: unknown }>(
				'POST',
				`/sso/providers/${encodeURIComponent(provider.id)}/test`
			);
			result = {
				ok: r.ok,
				details: typeof r.details === 'string' ? r.details : JSON.stringify(r.details ?? '')
			};
		} catch (e) {
			result = { ok: false, details: errorText(e) };
		} finally {
			testing = false;
		}
	}
	async function setEnabled(enabled: boolean) {
		if (!provider) return;
		try {
			provider = (
				await api<{ provider: SSOProvider }>('PATCH', `/sso/providers/${encodeURIComponent(provider.id)}`, {
					enabled
				})
			).provider;
			toasts.push(enabled ? `${provider.display_name} is on.` : `${provider.display_name} is off.`);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
	}
	async function remove() {
		if (!provider) return;
		try {
			await api('DELETE', `/sso/providers/${encodeURIComponent(provider.id)}`);
			toasts.push(`Removed ${provider.display_name}.`);
			goto('/sso');
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
	}
</script>

<svelte:head><title>{provider?.display_name ?? 'Provider'} · boson admin</title></svelte:head>

<p class="crumb"><a href="/sso">Sign-in providers</a></p>

{#if unsupported}
	<State kind="unsupported" title="Sign-in providers need a newer auth service"><p>{unsupported}.</p></State>
{:else if error}
	<State kind="error">
		{error}
		{#snippet action()}<a class="btn btn-small" href="/sso">All providers</a>{/snippet}
	</State>
{:else if !provider || !preset}
	<State kind="loading" />
{:else}
	<div class="page-head">
		<div>
			<h1>
				{provider.display_name}
				<span class="tag">{provider.kind === 'oidc' ? 'OIDC' : 'SAML'}</span>
				{#if provider.enabled}<span class="tag tag-ok">On</span>{:else}<span class="tag">Off</span>{/if}
			</h1>
			<p>
				{#if provider.enabled}People see “Sign in with {provider.display_name}” on the sign-in page.
				{:else}Switched off: nobody can sign in with it yet. Finish the setup, test it, then switch it on.{/if}
			</p>
		</div>
		<div class="actions">
			<button class="btn btn-small" onclick={test} disabled={testing}
				>{testing ? 'Testing…' : 'Test connection'}</button
			>
			{#if provider.enabled}
				<Confirm
					title="Switch off {provider.display_name}?"
					action="Switch off"
					danger
					onconfirm={() => setEnabled(false)}
				>
					{#snippet trigger(open)}<button class="btn btn-small" onclick={open}>Switch off</button>{/snippet}
					The button disappears from the sign-in page. People already signed in stay signed in; people who only
					ever used {provider.display_name} can’t sign in until it’s back on or they reset a password.
				</Confirm>
			{:else}
				<button class="btn btn-small btn-primary" onclick={() => setEnabled(true)}>Switch on</button>
			{/if}
		</div>
	</div>

	{#if result}
		<p class="result" class:ok={result.ok} role="status">
			<strong>{result.ok ? 'Connection works.' : 'Test failed.'}</strong>
			{result.details}
		</p>
	{/if}

	<div class="cols">
		<section class="panel" aria-labelledby="urls-h">
			<h2 id="urls-h">
				Give these to {preset.label === 'Other OpenID Connect' || preset.label === 'Other SAML'
					? 'your identity provider'
					: preset.label}
			</h2>
			{#if urls.length}
				<div class="urls">
					{#each urls as k (k)}<CopyField label={urlLabels[k]} value={provider[k] ?? ''} />{/each}
				</div>
			{:else}
				<p class="muted small">The auth service didn’t return the provider’s URLs.</p>
			{/if}
			<details class="howto" open={!provider.enabled}>
				<summary>Setup steps for {preset.label}</summary>
				<div class="howto-body"><SetupGuide {preset} {provider} /></div>
			</details>
		</section>

		<section aria-labelledby="edit-h">
			<h2 id="edit-h" class="sr-only">Settings</h2>
			{#key formKey}
				<ProviderForm
					{provider}
					onsaved={(p) => {
						provider = p;
						formKey++;
					}}
				/>
			{/key}
		</section>
	</div>

	<section class="section danger" aria-labelledby="del-h">
		<div class="section-head">
			<h2 id="del-h">Remove provider</h2>
		</div>
		<div class="del">
			<p>
				Linked sign-ins are removed with it. Accounts stay, but people who only used {provider.display_name} need
				a password reset to get back in.
			</p>
			<Confirm
				title="Remove {provider.display_name}?"
				action="Remove provider"
				danger
				typed={provider.slug}
				onconfirm={remove}
			>
				{#snippet trigger(open)}<button class="btn btn-small btn-danger" onclick={open}
						>Remove provider</button
					>{/snippet}
				This can’t be undone. Every account’s link to {provider.display_name} is deleted.
			</Confirm>
		</div>
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
	}
	.actions {
		display: flex;
		gap: var(--space-2);
		flex-wrap: wrap;
	}
	.cols {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(280px, 380px);
		gap: var(--space-8);
		align-items: start;
	}
	.cols > :first-child {
		order: 2;
		position: sticky;
		top: var(--space-6);
	}
	.panel {
		display: grid;
		gap: var(--space-4);
		padding: var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.panel h2 {
		font-size: var(--text-base);
	}
	.urls {
		display: grid;
		gap: var(--space-3);
	}
	.small {
		font-size: var(--text-sm);
	}
	.howto {
		border-top: 1px solid var(--border-soft);
		padding-top: var(--space-3);
	}
	summary {
		cursor: pointer;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--fg-2);
	}
	.howto-body {
		margin-top: var(--space-4);
	}
	.result {
		margin-bottom: var(--space-6);
		font-size: var(--text-sm);
		font-family: var(--font-mono);
		padding: var(--space-3) var(--space-4);
		border-left: 3px solid var(--danger);
		background: color-mix(in oklab, var(--danger), transparent 92%);
		color: var(--fg-2);
		overflow-wrap: anywhere;
	}
	.result strong {
		color: var(--danger-text);
	}
	.result.ok {
		border-left-color: var(--success);
		background: color-mix(in oklab, var(--success), transparent 92%);
	}
	.result.ok strong {
		color: var(--success-text);
	}
	.del {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		align-items: center;
		justify-content: space-between;
		padding: var(--space-4);
		border: 1px solid color-mix(in oklab, var(--danger), transparent 70%);
		border-radius: var(--radius-sm);
	}
	.del p {
		color: var(--fg-2);
		font-size: var(--text-sm);
		max-width: 64ch;
	}
	@media (max-width: 1100px) {
		.cols {
			grid-template-columns: minmax(0, 1fr);
		}
		.cols > :first-child {
			order: 0;
			position: static;
		}
	}
</style>
