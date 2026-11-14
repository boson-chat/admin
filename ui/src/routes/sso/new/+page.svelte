<script lang="ts">
	import { onMount } from 'svelte';
	import { api, isUnsupported } from '$lib/api';
	import { errorText } from '$lib/toasts.svelte';
	import ProviderForm from '$lib/components/ProviderForm.svelte';
	import State from '$lib/components/State.svelte';

	// Check the auth service has the provider API before showing a form.
	let ready = $state(false);
	let unsupported = $state('');
	let error = $state('');
	onMount(async () => {
		try {
			await api('GET', '/sso/providers');
			ready = true;
		} catch (e) {
			if (isUnsupported(e)) unsupported = e.message;
			else error = errorText(e);
		}
	});
</script>

<svelte:head><title>Add sign-in provider · boson admin</title></svelte:head>

<p class="crumb"><a href="/sso">Sign-in providers</a></p>
<div class="page-head">
	<div>
		<h1>Add a sign-in provider</h1>
		<p>
			Pick where people already have accounts, follow the steps on that side, then fill in what it gives you.
		</p>
	</div>
</div>

{#if unsupported}
	<State kind="unsupported" title="Sign-in providers need a newer auth service"><p>{unsupported}.</p></State>
{:else if error}
	<State kind="error">{error}</State>
{:else if !ready}
	<State kind="loading" />
{:else}
	<ProviderForm />
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
</style>
