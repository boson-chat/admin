<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { stackConfig, configGroups, label } from '$lib/stackconfig.svelte';
	import type { ConfigKey, ConfigRaw, ConfigValue, ConfigView } from '$lib/types';
	import ConfigField from '$lib/components/ConfigField.svelte';
	import ConfigStatus from '$lib/components/ConfigStatus.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';

	let view = $derived(stackConfig.view);
	let loadError = $state('');
	let drafts = $state<Record<string, ConfigValue | undefined>>({});
	let fieldErrors = $state<Record<string, string>>({});
	let saving = $state('');
	let resetting = $state('');
	let checking = $state(false);

	const base = (k: ConfigKey): ConfigValue | undefined => (k.set ? k.value : k.default);
	const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

	function fill(v: ConfigView | null) {
		if (!v) return;
		const next: Record<string, ConfigValue | undefined> = {};
		for (const k of v.keys) next[k.key] = structuredClone($state.snapshot(base(k)));
		drafts = next;
		fieldErrors = {};
	}

	async function load() {
		loadError = '';
		const v = await stackConfig.load();
		if (!v) loadError = stackConfig.error;
		fill(v);
		if (v?.enabled) recheck();
	}
	onMount(load);

	let groups = $derived(
		(view?.keys ?? []).length
			? configGroups.map((g) => ({
					...g,
					keys: (view?.keys ?? []).filter((k) => g.prefix.some((p) => k.key.startsWith(p)))
				}))
			: []
	);
	function dirtyKeys(keys: ConfigKey[]) {
		return keys.filter((k) => drafts[k.key] !== undefined && !same(drafts[k.key], base(k)));
	}

	// The file was changed after the panel's last save: by hand.
	let handEdited = $derived.by(() => {
		if (!view?.modified_at) return false;
		if (!view.saved_at) return true;
		return new Date(view.modified_at).getTime() - new Date(view.saved_at).getTime() > 2000;
	});

	async function recheck() {
		checking = true;
		try {
			await stackConfig.checkServices();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			checking = false;
		}
	}
	// After a save, ask again until nobody is still loading the old file.
	async function follow() {
		for (const wait of [800, 2000, 4000, 8000]) {
			await new Promise((r) => setTimeout(r, wait));
			await recheck();
			if (!stackConfig.statuses?.some((s) => s.state === 'pending')) return;
		}
	}

	function keyError(e: unknown, keys: string[]) {
		const msg = errorText(e);
		const hit = keys.find((k) => msg.startsWith(k + ':'));
		if (hit) fieldErrors = { ...fieldErrors, [hit]: msg.slice(hit.length + 1).trim() };
		toasts.push(msg, 'bad');
	}

	async function save(id: string, keys: ConfigKey[]) {
		const changed = dirtyKeys(keys);
		if (!changed.length) return;
		saving = id;
		fieldErrors = {};
		const set: Record<string, ConfigValue> = {};
		for (const k of changed) set[k.key] = $state.snapshot(drafts[k.key]) as ConfigValue;
		try {
			fill(await stackConfig.save({ set }));
			toasts.push(
				changed.length === 1 ? `Saved ${label(changed[0].key)}.` : `Saved ${changed.length} settings.`
			);
			follow();
		} catch (e) {
			keyError(
				e,
				changed.map((k) => k.key)
			);
			if (e instanceof Error && /changed since you loaded/.test(e.message)) await stackConfig.load();
		} finally {
			saving = '';
		}
	}
	function discard(keys: ConfigKey[]) {
		for (const k of keys) drafts[k.key] = structuredClone($state.snapshot(base(k)));
		fieldErrors = {};
	}
	async function reset(k: ConfigKey) {
		resetting = k.key;
		try {
			fill(await stackConfig.save({ unset: [k.key] }));
			toasts.push(`${label(k.key)} is back to its default.`);
			follow();
		} catch (e) {
			keyError(e, [k.key]);
		} finally {
			resetting = '';
		}
	}

	// the file itself
	let raw = $state<ConfigRaw | null>(null);
	let rawError = $state('');
	async function loadRaw() {
		try {
			raw = await api<ConfigRaw>('GET', '/config/raw');
			rawError = '';
		} catch (e) {
			rawError = errorText(e);
		}
	}
	let lines = $derived(raw ? raw.content.replace(/\n$/, '').split('\n') : []);
	let badLine = $derived(raw?.error?.line ?? view?.error?.line ?? 0);
	let issueLines = $derived(new Set((view?.issues ?? []).map((i) => i.line ?? 0)));

	const deployment: { env: string; what: string }[] = [
		{ env: 'ADMIN_DIRECTORY_API_URL', what: 'Which boson directory the Directory page talks to.' },
		{ env: 'ADMIN_METRICS_URL', what: 'The metrics backend for live numbers on the Stack page.' },
		{
			env: 'ADMIN_DOCKER_PROXY_URL',
			what: 'The Docker socket proxy behind Services, the Restart buttons and which services the Stack page shows.'
		},
		{ env: 'ADMIN_BOSON_API_URL', what: 'The boson API for profiles on user pages.' },
		{ env: 'ADMIN_IRC_CHECK', what: 'The IRC server the Stack page connects to.' },
		{
			env: 'ADMIN_HEALTH_TARGETS',
			what: 'Extra or replacement health URLs for the Stack page (built in for the stack’s services).'
		},
		{ env: 'CERT_HOSTS', what: 'The hostnames whose certificates the Stack page checks.' }
	];
</script>

<svelte:head><title>Stack config · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Stack config</h1>
		<p>
			The stack’s runtime settings live in one file on the server. Change them here or edit the file by hand;
			services pick up changes within seconds.
		</p>
	</div>
</div>

{#if loadError}
	<State kind="error">
		{loadError}
		{#snippet action()}<button class="btn btn-small" onclick={load}>Try again</button>{/snippet}
	</State>
{:else if !view}
	<State kind="loading" title="Reading the config file…" />
{:else if !view.enabled}
	<State kind="off" title="There’s no config file yet">
		<p>
			Every setting comes from the stack’s <code>.env</code> and needs a restart to change. To edit them here,
			update the stack (it adds <code>config/boson.yaml</code>, mounted into each service) or set
			<code>BOSON_CONFIG=/config/boson.yaml</code> on the admin service and mount the stack’s
			<code>config/</code>
			directory. The stack’s <code>docs/config.md</code> has the details.
		</p>
	</State>
{:else}
	<section class="file" aria-labelledby="file-h">
		<h2 id="file-h" class="sr-only">The file</h2>
		<p class="path"><code>{view.path}</code></p>
		<p class="meta">
			{#if !view.exists}
				Not created yet: the first save creates it.
			{:else if handEdited && view.modified_at}
				Edited by hand <Time at={view.modified_at} />.
			{:else if view.saved_at}
				Last saved here by {view.saved_by || 'an admin'} <Time at={view.saved_at} />.
			{:else if view.modified_at}
				Changed <Time at={view.modified_at} />.
			{/if}
			Values in the file win over the environment; Reset to default removes them.
		</p>
	</section>

	{#if view.error}
		<div class="banner">
			<Notice tone="bad">
				<strong
					>The file has an error{view.error.line ? ` on line ${view.error.line}` : ''}{view.error.column
						? `, column ${view.error.column}`
						: ''}:</strong
				>
				{view.error.message}. Every service keeps its last good settings, and the panel won’t save until the
				file is fixed by hand.
				<a href="#raw-h">Show the file</a>
			</Notice>
		</div>
	{/if}
	{#if view.issues.length}
		<div class="banner">
			<Notice tone="warn">
				{view.issues.length === 1 ? 'One value doesn’t work' : `${view.issues.length} values don’t work`}; the
				service keeps its last good one:
				<ul class="issues">
					{#each view.issues as i (i.key)}
						<li><span class="mono">line {i.line}</span> {i.message}</li>
					{/each}
				</ul>
			</Notice>
		</div>
	{/if}

	<section class="section" aria-labelledby="svc-h">
		<div class="section-head">
			<h2 id="svc-h">Services</h2>
			<p>Whether each service has loaded the current file.</p>
		</div>
		{#if stackConfig.statuses}
			<ConfigStatus statuses={stackConfig.statuses} {checking} onrecheck={recheck} />
		{:else}
			<p class="muted small">Asking the services…</p>
		{/if}
	</section>

	{#each groups as g (g.id)}
		{@const dirty = dirtyKeys(g.keys)}
		<section class="section" aria-labelledby="g-{g.id}">
			<div class="section-head">
				<h2 id="g-{g.id}">{g.title}</h2>
				<p>{g.detail}</p>
			</div>
			<form
				class="card"
				class:dirty={dirty.length > 0}
				onsubmit={(e) => {
					e.preventDefault();
					save(g.id, g.keys);
				}}
			>
				{#each g.keys as k (k.key)}
					<ConfigField
						{k}
						bind:value={drafts[k.key]}
						error={fieldErrors[k.key]}
						resetting={resetting === k.key}
						onreset={() => reset(k)}
					/>
				{/each}
				{#if g.keys.some((k) => k.editable)}
					<div class="bar">
						<p class="count" aria-live="polite">
							{#if dirty.length}{dirty.length === 1
									? 'One unsaved change'
									: `${dirty.length} unsaved changes`}{/if}
						</p>
						<div class="btns">
							<button
								class="btn btn-ghost btn-small"
								type="button"
								disabled={!dirty.length || saving === g.id}
								onclick={() => discard(g.keys)}>Discard</button
							>
							<button class="btn btn-primary btn-small" disabled={!dirty.length || saving === g.id}
								>{saving === g.id ? 'Saving…' : 'Save changes'}</button
							>
						</div>
					</div>
				{/if}
			</form>
		</section>
	{/each}

	<section class="section" aria-labelledby="more-h">
		<div class="section-head">
			<h2 id="more-h">Elsewhere in the panel</h2>
		</div>
		<p class="small">
			Sign-up, guest access and channel rules are on <a href="/settings">Network settings</a>; the stack
			directory’s name, contact and federation on <a href="/directory">Directory</a>. Both save to this file
			too.
		</p>
	</section>

	<section class="section" aria-labelledby="raw-h">
		<div class="section-head">
			<h2 id="raw-h">The file</h2>
			<p>
				Read-only here. On the server: <code>sudo $EDITOR config/boson.yaml</code> in the stack directory.
			</p>
		</div>
		<details
			ontoggle={(e) => {
				if (e.currentTarget.open) loadRaw();
			}}
			open={!!view.error}
		>
			<summary>Show {view.path}</summary>
			{#if rawError}
				<Notice tone="bad">{rawError}</Notice>
			{:else if raw}
				{#if !raw.exists}
					<p class="muted small">The file doesn’t exist yet.</p>
				{:else}
					<pre class="raw" aria-label="Contents of {raw.path}"><code
							>{#each lines as l, i (i)}<span
									class="ln"
									class:bad={i + 1 === badLine}
									class:warn={issueLines.has(i + 1)}
									><span class="no" aria-hidden="true">{i + 1}</span>{l || ' '}</span
								>{/each}</code
						></pre>
				{/if}
			{:else}
				<p class="muted small">Loading…</p>
			{/if}
		</details>
	</section>

	<section class="section" aria-labelledby="env-h">
		<div class="section-head">
			<h2 id="env-h">Deployment settings</h2>
			<p>
				These connect the panel to the rest of the stack, so they stay in the stack’s <code>.env</code>. After
				changing one, run <code>docker compose up -d admin</code>.
			</p>
		</div>
		<dl class="env">
			{#each deployment as d (d.env)}
				<dt><code>{d.env}</code></dt>
				<dd>{d.what}</dd>
			{/each}
		</dl>
	</section>
{/if}

<style>
	.file {
		display: grid;
		gap: var(--space-1);
		max-width: 760px;
	}
	.path code {
		font-size: var(--text-base);
		color: var(--fg);
	}
	.meta {
		font-size: var(--text-sm);
		color: var(--fg-quiet);
	}
	.banner {
		margin-top: var(--space-5);
		max-width: 760px;
	}
	.issues {
		margin: var(--space-2) 0 0;
		padding-left: var(--space-5);
	}
	.section {
		max-width: 760px;
	}
	.card {
		display: grid;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.card.dirty {
		border-color: color-mix(in oklab, var(--accent), var(--border) 50%);
	}
	.bar {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-4);
		align-items: center;
		justify-content: space-between;
		padding: var(--space-3) var(--space-4);
		border-top: 1px solid var(--border);
	}
	.count {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--accent-text);
	}
	.btns {
		display: flex;
		gap: var(--space-2);
		margin-left: auto;
	}
	.small {
		font-size: var(--text-sm);
	}
	summary {
		cursor: pointer;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--fg-2);
		margin-bottom: var(--space-3);
	}
	.raw {
		margin: 0;
		max-height: 32rem;
		overflow: auto;
		padding: var(--space-3) 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		line-height: 1.6;
	}
	.ln {
		display: block;
		padding-right: var(--space-4);
		white-space: pre;
	}
	.ln.bad {
		background: color-mix(in oklab, var(--danger), transparent 85%);
	}
	.ln.warn {
		background: color-mix(in oklab, var(--warn), transparent 88%);
	}
	.no {
		display: inline-block;
		width: 3.5em;
		padding-right: var(--space-3);
		text-align: right;
		color: var(--fg-quiet);
		user-select: none;
	}
	.env {
		display: grid;
		grid-template-columns: max-content minmax(0, 1fr);
		gap: var(--space-2) var(--space-4);
		margin: 0;
		font-size: var(--text-sm);
	}
	.env dd {
		margin: 0;
		color: var(--fg-2);
	}
	@media (max-width: 640px) {
		.env {
			grid-template-columns: minmax(0, 1fr);
		}
		.env dd {
			margin-bottom: var(--space-2);
		}
	}
</style>
