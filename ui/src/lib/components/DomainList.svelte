<script lang="ts">
	import { cleanDomain, domainProblem } from '$lib/federation';

	/**
	 * A list of home domains (boson.chat) to add to and remove from. Paste
	 * an identity or a URL and it keeps the domain.
	 */
	let {
		id,
		label,
		items = $bindable(),
		other,
		placeholder = 'boson.chat',
		empty,
		disabled = false,
		error = ''
	}: {
		id: string;
		label: string;
		items: string[];
		/** The other list, which a domain can't also be on. */
		other?: { name: string; list: string[] };
		placeholder?: string;
		empty: string;
		disabled?: boolean;
		error?: string;
	} = $props();

	let input = $state('');
	let problem = $derived(domainProblem(input, items, other));

	function add(e?: Event) {
		e?.preventDefault();
		const d = cleanDomain(input);
		if (!d || problem) return;
		items = [...items, d];
		input = '';
	}
	function remove(d: string) {
		items = items.filter((x) => x !== d);
	}
	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') add(e);
	}
</script>

<div class="domains">
	<div class="add">
		<label class="sr-only" for={id}>Add to the {label}</label>
		<input
			{id}
			type="text"
			inputmode="url"
			bind:value={input}
			{onkeydown}
			{placeholder}
			{disabled}
			autocomplete="off"
			autocapitalize="off"
			spellcheck="false"
			aria-invalid={!!problem || !!error}
			aria-describedby={problem ? `${id}-p` : error ? `${id}-e` : undefined}
		/>
		<button
			class="btn btn-small"
			type="button"
			onclick={add}
			disabled={disabled || !input.trim() || !!problem}>Add</button
		>
	</div>
	{#if problem}<p class="err" id="{id}-p" role="alert">{problem}</p>{/if}
	{#if error && !problem}<p class="err" id="{id}-e">{error}</p>{/if}
	{#if items.length}
		<ul aria-label={label}>
			{#each items as d (d)}
				<li>
					<span class="d">{d}</span>
					<button
						class="x"
						type="button"
						onclick={() => remove(d)}
						{disabled}
						aria-label="Remove {d} from the {label}">×</button
					>
				</li>
			{/each}
		</ul>
	{:else}
		<p class="none">{empty}</p>
	{/if}
</div>

<style>
	.domains {
		display: grid;
		gap: var(--space-2);
	}
	.add {
		display: flex;
		gap: var(--space-2);
	}
	.add input {
		flex: 1;
		min-width: 0;
		font-family: var(--font-mono);
	}
	input[aria-invalid='true'] {
		border-color: var(--danger);
	}
	ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	li {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		padding: 2px 4px 2px var(--space-2);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface-raised);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		max-width: 100%;
	}
	.d {
		overflow-wrap: anywhere;
	}
	.x {
		border: 0;
		background: none;
		color: var(--fg-quiet);
		font-size: var(--text-base);
		line-height: 1;
		width: 24px;
		height: 24px;
		border-radius: var(--radius-sm);
		cursor: pointer;
	}
	.x:hover:not(:disabled) {
		color: var(--danger-text);
		background: var(--surface-hover);
	}
	.none {
		font-size: var(--text-sm);
		color: var(--fg-quiet);
	}
	.err {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		line-height: 1.4;
		color: var(--danger-text);
	}
</style>
