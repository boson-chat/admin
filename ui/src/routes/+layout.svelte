<script lang="ts">
	import '@fontsource-variable/inter';
	import '@fontsource-variable/jetbrains-mono';
	import '../app.css';
	import type { Snippet } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import type { SessionInfo } from '$lib/types';
	import Shell from '$lib/components/Shell.svelte';
	import Toaster from '$lib/components/Toaster.svelte';
	import State from '$lib/components/State.svelte';
	import { firstAllowed, pagePerms, permFor } from '$lib/perms';

	let { children }: { children: Snippet } = $props();
	let bootError = $state('');

	let onLogin = $derived(page.url.pathname === '/login');
	// The permission this page needs, when the account lacks it.
	let missing = $derived.by(() => {
		const p = permFor(page.url.pathname);
		return p && session.info && !session.can(p) ? p : '';
	});
	let landing = $derived(session.info ? firstAllowed(session.info.permissions) : null);

	// The stack overview needs services.view; send everyone else to their first page.
	$effect(() => {
		if (missing && page.url.pathname === '/' && landing && landing !== '/')
			goto(landing, { replaceState: true });
	});

	$effect(() => {
		if (session.loaded) return;
		api<SessionInfo>('GET', '/session')
			.then((s) => session.set(s))
			.catch((e) => {
				if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
					session.clear(e.status === 403 ? e.message : '');
				} else {
					bootError = e instanceof Error ? e.message : String(e);
				}
			});
	});

	$effect(() => {
		if (session.loaded && !session.info && !onLogin) {
			const next = page.url.pathname + page.url.search;
			goto('/login' + (next !== '/' ? '?next=' + encodeURIComponent(next) : ''), { replaceState: true });
		}
	});
</script>

{#if bootError}
	<div class="boot">
		<h1>The panel can't reach its server</h1>
		<p>{bootError}</p>
		<button class="btn" onclick={() => location.reload()}>Reload</button>
	</div>
{:else if onLogin}
	{@render children()}
{:else if session.info}
	<Shell>
		{#if missing}
			{@const pg = pagePerms.find((p) => p.perm === missing)}
			<div class="page-head">
				<div>
					<h1>{pg?.label ?? 'Stack'}</h1>
				</div>
			</div>
			<State kind="off" title="Your roles don’t include this page">
				It needs the <code>{missing}</code> permission. Someone who manages roles can add it to one of your
				roles.
				{#snippet action()}
					{#if landing && landing !== page.url.pathname}<a class="btn btn-small" href={landing}
							>Go to a page you can open</a
						>{/if}
				{/snippet}
			</State>
		{:else}
			{@render children()}
		{/if}
	</Shell>
{:else}
	<div class="boot" aria-busy="true"><span class="muted">Loading…</span></div>
{/if}
<Toaster />

<style>
	.boot {
		min-height: 100vh;
		display: grid;
		place-content: center;
		gap: var(--space-3);
		padding: var(--space-4);
		text-align: center;
	}
	.boot h1 {
		font-size: var(--text-xl);
	}
	.boot p {
		color: var(--fg-2);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
	}
	.boot .btn {
		justify-self: center;
	}
</style>
