<script lang="ts">
	import type { Preset } from '$lib/sso';
	import { urlLabels } from '$lib/sso';
	import type { SSOProvider } from '$lib/types';
	import CopyField from './CopyField.svelte';

	/** Numbered setup steps for an identity provider, with boson's URLs inline. */
	let { preset, provider }: { preset: Preset; provider?: SSOProvider } = $props();
</script>

<ol class="guide">
	{#each preset.guide as step, i (i)}
		<li>
			<p>{step.text}</p>
			{#if step.url}
				{#if provider?.[step.url]}
					<CopyField
						label={urlLabels[step.url]}
						value={provider[step.url] ?? ''}
						multiline={step.url === 'sp_certificate'}
					/>
				{:else}
					<p class="later">
						{urlLabels[step.url]}: shown here once the provider is saved.
					</p>
				{/if}
			{/if}
		</li>
	{/each}
</ol>

<style>
	.guide {
		list-style: none;
		counter-reset: step;
		margin: 0;
		padding: 0;
		display: grid;
		gap: var(--space-4);
	}
	li {
		counter-increment: step;
		position: relative;
		display: grid;
		gap: var(--space-2);
		padding-left: var(--space-8);
		font-size: var(--text-sm);
		color: var(--fg-2);
		line-height: 1.55;
	}
	li::before {
		content: counter(step);
		position: absolute;
		left: 0;
		top: 0;
		width: 20px;
		height: 20px;
		display: grid;
		place-items: center;
		border-radius: 50%;
		border: 1px solid var(--border-strong);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--fg-2);
	}
	.later {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		padding: var(--space-2) var(--space-3);
		border: 1px dashed var(--border-strong);
		border-radius: var(--radius-sm);
	}
</style>
