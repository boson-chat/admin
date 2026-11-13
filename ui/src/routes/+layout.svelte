<script lang="ts">
	import '@fontsource-variable/atkinson-hyperlegible-next';
	import '@fontsource-variable/atkinson-hyperlegible-mono';
	import '../app.css';
	import type { Snippet } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import type { SessionInfo } from '$lib/types';
	import Shell from '$lib/components/Shell.svelte';
	import Toaster from '$lib/components/Toaster.svelte';

	let { children }: { children: Snippet } = $props();
	let bootError = $state('');

	let onLogin = $derived(page.url.pathname === '/login');

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
	<Shell>{@render children()}</Shell>
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
	.boot .btn {
		justify-self: center;
	}
</style>
