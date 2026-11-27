<script lang="ts">
	/** "Email isn't set up" and what it means here (auth has no SMTP server). */
	import { emailOffFor } from '$lib/accounts';
	import Notice from './Notice.svelte';
	let { backend = 'local', setupLink = true }: { backend?: string; setupLink?: boolean } = $props();
</script>

<Notice tone="warn">
	<p class="lead">
		<strong>Email isn’t set up.</strong> The auth service has no SMTP server, so it can’t send email.
	</p>
	<ul>
		{#each emailOffFor(backend) as line (line)}<li>{line}</li>{/each}
	</ul>
	{#if setupLink}
		<p>
			Set the SMTP server under <a href="/config#g-auth">Stack config</a> (or <code>SMTP_HOST</code> in .env) to
			turn these on.
		</p>
	{/if}
</Notice>

<style>
	p,
	ul {
		margin: 0;
	}
	ul {
		margin: var(--space-1) 0;
		padding-left: 1.2em;
	}
	.lead strong {
		color: var(--fg);
	}
</style>
