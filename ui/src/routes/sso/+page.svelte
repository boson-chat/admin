<script lang="ts">
	import { onMount } from 'svelte';
	import { api, isUnsupported } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import type { SSOProvider } from '$lib/types';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';

	let providers = $state<SSOProvider[] | null>(null);
	let error = $state('');
	let unsupported = $state('');
	let tests = $state<Record<string, { busy: boolean; ok?: boolean; details?: string }>>({});

	async function load() {
		error = '';
		try {
			providers = (await api<{ providers: SSOProvider[] }>('GET', '/sso/providers')).providers;
		} catch (e) {
			if (isUnsupported(e)) unsupported = e.message;
			else error = errorText(e);
		}
	}
	onMount(load);

	function detailText(d: unknown): string {
		if (d === undefined || d === null) return '';
		return typeof d === 'string' ? d : JSON.stringify(d);
	}

	async function test(p: SSOProvider) {
		tests[p.id] = { busy: true };
		try {
			const r = await api<{ ok: boolean; details?: unknown }>(
				'POST',
				`/sso/providers/${encodeURIComponent(p.id)}/test`
			);
			tests[p.id] = { busy: false, ok: r.ok, details: detailText(r.details) };
		} catch (e) {
			tests[p.id] = { busy: false, ok: false, details: errorText(e) };
		}
	}

	async function setEnabled(p: SSOProvider, enabled: boolean) {
		try {
			const r = await api<{ provider: SSOProvider }>('PATCH', `/sso/providers/${encodeURIComponent(p.id)}`, {
				enabled
			});
			providers = providers?.map((x) => (x.id === p.id ? r.provider : x)) ?? null;
			toasts.push(
				enabled ? `${p.display_name} is on: people can sign in with it.` : `${p.display_name} is off.`
			);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
	}
</script>

<svelte:head><title>Sign-in providers · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Sign-in providers</h1>
		<p>
			Let people sign in with an existing Google, company or community account (OpenID Connect or SAML) next
			to their boson password.
		</p>
	</div>
	{#if providers}<a class="btn btn-primary" href="/sso/new">Add provider</a>{/if}
</div>

{#if unsupported}
	<State kind="unsupported" title="Sign-in providers need a newer auth service">
		<p>{unsupported}.</p>
		<p>Everyone signs in with a username and password until then.</p>
	</State>
{:else if error}
	<State kind="error">
		{error}
		{#snippet action()}<button class="btn btn-small" onclick={load}>Try again</button>{/snippet}
	</State>
{:else if providers === null}
	<State kind="loading" title="Loading providers…" />
{:else if providers.length === 0}
	<State kind="empty" title="No sign-in providers">
		Everyone signs in with a username and password. Add Google, Keycloak, Okta or any OpenID Connect or SAML
		provider to offer “Sign in with …”.
		{#snippet action()}<a class="btn btn-small btn-primary" href="/sso/new">Add provider</a>{/snippet}
	</State>
{:else}
	<ul class="plist">
		{#each providers as p (p.id)}
			{@const t = tests[p.id]}
			<li class:off={!p.enabled}>
				<div class="main">
					<a class="name" href="/sso/{encodeURIComponent(p.id)}">{p.display_name}</a>
					<span class="tag">{p.kind === 'oidc' ? 'OIDC' : 'SAML'}</span>
					{#if p.enabled}<span class="tag tag-ok">On</span>{:else}<span class="tag">Off</span>{/if}
					<div class="meta">
						<code>{p.slug}</code>
						<span>{p.allow_signup ? 'Creates accounts' : 'Existing accounts only'}</span>
						{#if p.trust_email}<span>Trusts email</span>{/if}
						{#if p.updated_at || p.created_at}<span>Changed <Time at={p.updated_at ?? p.created_at} /></span
							>{/if}
					</div>
				</div>
				<div class="acts">
					<button
						class="btn btn-small"
						onclick={() => test(p)}
						disabled={t?.busy}
						aria-label="Test {p.display_name}">{t?.busy ? 'Testing…' : 'Test'}</button
					>
					<button class="btn btn-small" onclick={() => setEnabled(p, !p.enabled)}
						>{p.enabled ? 'Switch off' : 'Switch on'}</button
					>
					<a class="btn btn-small btn-ghost" href="/sso/{encodeURIComponent(p.id)}">Edit</a>
				</div>
				{#if t && !t.busy}
					<p class="result" class:ok={t.ok} role="status">
						<strong>{t.ok ? 'Works.' : 'Failed.'}</strong>
						{t.details}
					</p>
				{/if}
			</li>
		{/each}
	</ul>
{/if}

<style>
	.plist {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	li {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: var(--space-3) var(--space-4);
		align-items: center;
		padding: var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}
	li:last-child {
		border-bottom: 0;
	}
	.main {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		min-width: 0;
	}
	.name {
		font-family: var(--font-mono);
		font-weight: 600;
		color: var(--fg);
		text-decoration: none;
	}
	.name:hover {
		color: var(--accent-text);
		text-decoration: underline;
	}
	.off .name {
		color: var(--fg-2);
	}
	.meta {
		flex-basis: 100%;
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-4);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.acts {
		display: flex;
		gap: var(--space-2);
		flex-wrap: wrap;
		justify-content: flex-end;
	}
	.result {
		grid-column: 1 / -1;
		font-size: var(--text-sm);
		font-family: var(--font-mono);
		padding: var(--space-2) var(--space-3);
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
	@media (max-width: 640px) {
		li {
			grid-template-columns: minmax(0, 1fr);
		}
		.acts {
			justify-content: flex-start;
		}
	}
</style>
