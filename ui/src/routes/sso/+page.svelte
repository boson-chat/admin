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
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.list li {
		display: flex;
		align-items: baseline;
		gap: var(--space-3);
		flex-wrap: wrap;
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}
	.list li:last-child {
		border-bottom: 0;
	}
	.list strong {
		font-family: var(--font-mono);
	}
	/* the roadmap is a real sequence (auth phases 2, 3), drawn as a dashed track */
	.plan {
		list-style: none;
		margin: 0 0 0 7px;
		padding: 0;
		display: grid;
		gap: 0;
		border-left: 2px dashed var(--border-strong);
	}
	.plan li {
		padding: var(--space-3) 0 var(--space-5) var(--space-6);
		position: relative;
		max-width: 72ch;
	}
	.plan li::before {
		content: '';
		position: absolute;
		left: -8px;
		top: 16px;
		width: 14px;
		height: 14px;
		border-radius: 50%;
		background: var(--bg);
		border: 2px dashed var(--fg-3);
	}
	.phase {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		color: var(--fg-quiet);
	}
	.plan h3 {
		margin: var(--space-1) 0;
	}
	.plan p {
		font-size: var(--text-sm);
		color: var(--fg-2);
	}
	.after {
		margin-top: var(--space-4);
		font-size: var(--text-sm);
		max-width: 70ch;
	}
</style>
