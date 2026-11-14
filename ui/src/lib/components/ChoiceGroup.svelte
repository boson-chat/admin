<script lang="ts" module>
	export interface Choice<T extends string> {
		value: T;
		label: string;
		detail: string;
		/** Shown next to the label, e.g. "Default". */
		note?: string;
	}
</script>

<script lang="ts" generics="V extends string">
	/**
	 * A radio group drawn as rows: each choice says what it means. Native
	 * radios, so arrow keys, forms and screen readers work as usual.
	 */
	let {
		legend,
		hint,
		choices,
		value = $bindable(),
		name,
		disabled = false
	}: {
		legend: string;
		hint?: string;
		choices: Choice<V>[];
		value: V;
		name: string;
		disabled?: boolean;
	} = $props();
</script>

<fieldset class="group" {disabled}>
	<legend>{legend}</legend>
	{#if hint}<p class="hint">{hint}</p>{/if}
	<div class="choices">
		{#each choices as c (c.value)}
			<label class="choice" class:on={value === c.value}>
				<input type="radio" {name} value={c.value} bind:group={value} />
				<span class="mark" aria-hidden="true"></span>
				<span class="txt">
					<span class="label"
						>{c.label}{#if c.note}<span class="note">{c.note}</span>{/if}</span
					>
					<span class="detail">{c.detail}</span>
				</span>
			</label>
		{/each}
	</div>
</fieldset>

<style>
	fieldset {
		border: 0;
		margin: 0;
		padding: 0;
		min-width: 0;
	}
	legend {
		padding: 0;
		font-family: var(--font-display);
		font-weight: 600;
		font-size: var(--text-base);
		color: var(--fg);
	}
	.hint {
		margin-top: var(--space-1);
		color: var(--fg-quiet);
		font-size: var(--text-sm);
		max-width: 64ch;
	}
	.choices {
		display: grid;
		margin-top: var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.choice {
		position: relative;
		display: grid;
		grid-template-columns: 16px minmax(0, 1fr);
		gap: var(--space-3);
		align-items: start;
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--border-soft);
		cursor: pointer;
		transition: background-color var(--motion-fast) var(--ease-standard);
	}
	.choice:last-child {
		border-bottom: 0;
	}
	.choice:hover {
		background: var(--surface-hover);
	}
	.choice.on {
		background: color-mix(in oklab, var(--accent), transparent 93%);
	}
	.choice.on::before {
		content: '';
		position: absolute;
		left: -1px;
		top: -1px;
		bottom: -1px;
		width: 2px;
		background: var(--accent);
	}
	input {
		position: absolute;
		opacity: 0;
		width: 1px;
		height: 1px;
	}
	.mark {
		width: 16px;
		height: 16px;
		margin-top: 2px;
		border-radius: 50%;
		border: 1.5px solid var(--border-strong);
		background: var(--bg);
		transition: border-color var(--motion-fast) var(--ease-standard);
	}
	.on .mark {
		border-color: var(--accent);
		box-shadow:
			inset 0 0 0 3px var(--bg),
			inset 0 0 0 8px var(--accent);
	}
	input:focus-visible + .mark {
		box-shadow: var(--focus-ring);
	}
	.on input:focus-visible + .mark {
		box-shadow:
			inset 0 0 0 3px var(--bg),
			inset 0 0 0 8px var(--accent),
			var(--focus-ring);
	}
	.txt {
		display: grid;
		gap: 2px;
	}
	.label {
		font-family: var(--font-mono);
		font-weight: 600;
		font-size: var(--text-sm);
		color: var(--fg);
		display: inline-flex;
		gap: var(--space-2);
		align-items: baseline;
	}
	.note {
		font-weight: 500;
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.detail {
		font-size: var(--text-sm);
		color: var(--fg-2);
		line-height: 1.5;
	}
	fieldset:disabled .choice {
		cursor: not-allowed;
		opacity: 0.6;
	}
</style>
