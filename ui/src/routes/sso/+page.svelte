<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { errorText } from '$lib/toasts.svelte';
	import Notice from '$lib/components/Notice.svelte';

	interface Provider {
		slug: string;
		kind: string;
		display_name: string;
	}
	interface Phase {
		phase: string;
		kind: string;
		title: string;
		detail: string;
	}
	let data = $state<{ providers: Provider[]; roadmap: Phase[]; error: string } | null>(null);
	let error = $state('');

	onMount(async () => {
		try {
			data = await api('GET', '/sso');
		} catch (e) {
			error = errorText(e);
		}
	});
</script>

<svelte:head><title>Sign-in providers · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Sign-in providers</h1>
		<p>
			Let people sign in with an existing company or community account instead of a boson password. This
			section is a placeholder until the auth service supports single sign-on.
		</p>
	</div>
	<span class="tag">Coming later</span>
</div>

{#if error}
	<Notice>{error}</Notice>
{:else if data}
	<section aria-labelledby="cfg-h">
		<div class="section-head"><h2 id="cfg-h">Configured providers</h2></div>
		{#if data.providers.length}
			<ul class="list">
				{#each data.providers as p (p.slug)}
					<li><strong>{p.display_name}</strong> <span class="muted">{p.kind}</span> <code>{p.slug}</code></li>
				{/each}
			</ul>
		{:else}
			<p class="muted">None. Everyone signs in with a username and password.</p>
		{/if}
	</section>

	<section class="section" aria-labelledby="plan-h">
		<div class="section-head"><h2 id="plan-h">What’s planned</h2></div>
		<ol class="plan">
			{#each data.roadmap as r (r.phase)}
				<li>
					<span class="phase">Auth phase {r.phase}</span>
					<h3>{r.title}</h3>
					<p class="muted">{r.detail}</p>
				</li>
			{/each}
		</ol>
		<p class="muted after">
			Once the auth service has an admin API for providers, they’ll be added, tested and switched on from this
			page.
		</p>
	</section>
{/if}

<style>
	.list {
		list-style: none;
		padding: 0;
		margin: 0;
	}
	.plan {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 0;
		border-left: 2px dashed var(--line-strong);
	}
	.plan li {
		padding: var(--space-3) 0 var(--space-4) var(--space-5);
		position: relative;
	}
	.plan li::before {
		content: '';
		position: absolute;
		left: -7px;
		top: 18px;
		width: 12px;
		height: 12px;
		border-radius: 50%;
		background: var(--ink);
		border: 2px dashed var(--muted);
	}
	.phase {
		font-size: var(--t-xs);
		color: var(--muted);
	}
	.plan p {
		font-size: var(--t-sm);
	}
	.after {
		margin-top: var(--space-4);
		font-size: var(--t-sm);
		max-width: 70ch;
	}
</style>
