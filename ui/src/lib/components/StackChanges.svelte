<script lang="ts">
	// What the newest stack release changes on this install, worked out on
	// the server before anything is applied: files (with diffs), images,
	// .env pins, migrations, which services restart.
	import Notice from '$lib/components/Notice.svelte';
	import { diffClass, fileAction, localEdits, shortImage, type StackStatus } from '$lib/updates';

	let { s }: { s: StackStatus } = $props();

	let edits = $derived(localEdits(s));
	let unknown = $derived(s.files.filter((f) => f.action === 'unknown'));
	let pinMoves = $derived(s.pins.filter((p) => p.action === 'raise' || p.action === 'pin'));
	let pinKeeps = $derived(s.pins.filter((p) => p.action === 'keep' && p.pin && p.pin !== p.default));
	let pending = $derived(s.migrations.filter((m) => m.state === 'pending'));
</script>

<div class="stack" data-testid="stack-changes">
	{#if s.error}
		<Notice tone="bad"><strong>Can’t apply this release here:</strong> {s.error}</Notice>
	{/if}
	{#if s.pre_release}
		<Notice tone="info">
			This install is from before stack releases (no <code>VERSION</code>): its files were copied by hand.
			Every file that differs from release {s.version} is replaced{unknown.length
				? ` (${unknown.length} listed below as “differs”)`
				: ''}; the backup keeps the old ones. Changes of your own belong in
			<code>compose.override.yaml</code>, which an update never touches.
		</Notice>
	{/if}
	{#if edits.length}
		<Notice tone="warn">
			<strong
				>{edits.length === 1 ? 'A file was' : `${edits.length} files were`} changed on this server since they were
				installed:</strong
			>
			{edits.map((f) => f.path).join(', ')}. The update replaces {edits.length === 1 ? 'it' : 'them'} with the release’s
			and keeps your copy as <code>&lt;file&gt;.local</code>.
		</Notice>
	{/if}

	<dl class="facts">
		<div>
			<dt>Restarts</dt>
			<dd>
				{#if s.restarts.length}<span class="mono">{s.restarts.join(', ')}</span>{:else}nothing{/if}
				{#if s.self}<span class="muted">, then this panel (last)</span>{/if}
				{#if s.oneshots?.length}<span class="muted">; runs once: {s.oneshots.join(', ')}</span>{/if}
			</dd>
		</div>
		{#if s.kept?.length}
			<div>
				<dt>Never restarted</dt>
				<dd>
					<span class="mono">{s.kept.join(', ')}</span>
					<span class="muted"
						>— its configuration changes, but restarting it drops every bridged IRC connection; it picks the
						change up the next time it restarts.</span
					>
				</dd>
			</div>
		{/if}
		{#if s.removed?.length}
			<div>
				<dt>No longer in the stack</dt>
				<dd>
					<span class="mono">{s.removed.join(', ')}</span>
					<span class="muted"
						>(left running; <code>docker compose up -d --remove-orphans</code> removes it)</span
					>
				</dd>
			</div>
		{/if}
	</dl>

	{#if s.included.length}
		<h4>Included (built and versioned by the stack)</h4>
		<ul class="rows">
			{#each s.included as i (i.service)}
				<li>
					<strong>{i.name}</strong>
					{#if i.changed}
						<span class="mono">{shortImage(i.from)} → {shortImage(i.to)}</span>
					{:else}
						<span class="mono muted">{shortImage(i.to)}, unchanged</span>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}

	{#if s.images.length}
		<h4>Image changes</h4>
		<ul class="rows">
			{#each s.images as i (i.service)}
				<li>
					<strong>{i.service}</strong> <span class="mono">{shortImage(i.from)} → {shortImage(i.to)}</span>
				</li>
			{/each}
		</ul>
	{/if}

	{#if pinMoves.length || pinKeeps.length}
		<h4>Version pins in <code>.env</code></h4>
		<p class="muted note">
			A pin older than this release’s default is raised to it; a newer pin (a part you updated on its own) or
			another image is kept.
		</p>
		<ul class="rows">
			{#each [...pinMoves, ...pinKeeps] as p (p.env_var)}
				<li>
					<code>{p.env_var}</code>
					{#if p.action === 'raise'}<span class="mono">{shortImage(p.pin)} → {shortImage(p.to)}</span
						>{:else}<span class="mono">{shortImage(p.to)}</span>{/if}
					<span class="muted"
						>{p.note}{p.action === 'keep' ? ` (release: ${shortImage(p.default)})` : ''}</span
					>
				</li>
			{/each}
		</ul>
	{/if}

	{#if pending.length}
		<h4>Migrations ({pending.length})</h4>
		<ul class="rows">
			{#each pending as m (m.name)}
				<li><code>{m.name}</code> <span class="muted">{m.about}</span></li>
			{/each}
		</ul>
	{/if}

	<details class="files">
		<summary
			>Files: {s.files.length} changed{#if s.unchanged}, {s.unchanged} unchanged{/if}</summary
		>
		{#if !s.files.length}<p class="muted note">No file changes.</p>{/if}
		{#each s.files as f (f.path)}
			{@const a = fileAction[f.action]}
			<details class="file">
				<summary>
					<code>{f.path}</code>
					<span class="tag {a.tone}">{a.text}</span>
					{#if f.services?.length}<span class="muted">restarts {f.services.join(', ')}</span>{/if}
				</summary>
				{#if f.diff?.length}
					<pre class="diff" aria-label="Changes in {f.path}">{#each f.diff as l, i (i)}<span
								class={diffClass(l)}
								>{l}
</span>{/each}{#if f.truncated}<span class="muted">… (cut)</span>{/if}</pre>
				{:else}
					<p class="muted note">{f.action === 'remove' ? 'Removed.' : 'No diff.'}</p>
				{/if}
			</details>
		{/each}
	</details>
</div>

<style>
	.stack {
		margin: var(--space-2) 0;
		display: grid;
		gap: var(--space-2);
		min-width: 0;
	}
	/* Grid items: a long diff line scrolls inside its box, not the page. */
	.stack > * {
		min-width: 0;
	}
	h4 {
		font-size: var(--text-sm);
		margin: var(--space-2) 0 0;
	}
	.facts {
		display: grid;
		gap: var(--space-1);
		margin: 0;
		font-size: var(--text-sm);
	}
	.facts div {
		display: grid;
		grid-template-columns: 9.5rem minmax(0, 1fr);
		gap: var(--space-2);
	}
	.facts dt {
		color: var(--fg-quiet);
	}
	.facts dd {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.rows {
		list-style: none;
		padding: 0;
		margin: 0;
		display: grid;
		gap: var(--space-1);
		font-size: var(--text-sm);
	}
	.rows li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-2);
		align-items: baseline;
		overflow-wrap: anywhere;
	}
	.note {
		font-size: var(--text-sm);
		margin: 0;
	}
	.files > summary,
	.file > summary {
		cursor: pointer;
		font-size: var(--text-sm);
	}
	.file {
		margin: var(--space-1) 0 0 var(--space-3);
	}
	.file > summary {
		display: list-item;
		overflow-wrap: anywhere;
	}
	.file .tag {
		margin-left: var(--space-1);
	}
	.diff {
		max-width: 100%;
		margin: var(--space-1) 0 var(--space-2);
		max-height: 320px;
		overflow: auto;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		background: var(--bg);
		border: 1px solid var(--border-soft);
		padding: var(--space-2);
		white-space: pre;
	}
	.diff .dadd {
		color: var(--success-text);
	}
	.diff .ddel {
		color: var(--danger-text);
	}
	.diff .dhunk {
		color: var(--accent-text);
	}
	.diff .dh {
		color: var(--fg-quiet);
	}
	@media (max-width: 640px) {
		.facts div {
			grid-template-columns: minmax(0, 1fr);
			gap: 0;
		}
	}
</style>
