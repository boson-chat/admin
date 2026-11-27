<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { errorText, toasts } from '$lib/toasts.svelte';
	import { relative, datetime } from '$lib/format';
	import Notice from '$lib/components/Notice.svelte';
	import State from '$lib/components/State.svelte';
	import Dialog from '$lib/components/Dialog.svelte';
	import { updates } from '$lib/updates.svelte';
	import {
		behind,
		bytes,
		checkError,
		checkPending,
		checkPhase,
		impactWord,
		renderInline,
		renderMarkdown,
		running,
		selectable,
		withoutTitle,
		stateTag,
		stateWord,
		type ApplyMode,
		type ComponentStatus,
		type Preview,
		type Run
	} from '$lib/updates';

	let st = $derived(updates.status);
	let error = $state('');
	let checking = $state(false);
	let now = $state(Date.now());
	let picked = $state<Record<string, boolean>>({});
	let preview = $state<Preview | null>(null);
	let mode = $state<ApplyMode>('all');
	let ack = $state(false);
	let busy = $state(false);
	let dialog = $state<Dialog>();
	let rbDialog = $state<Dialog>();
	let restoreData = $state(false);
	let restarting = $state(false);
	let lost = $state(false);

	let canApply = $derived(session.can('services.manage', 'settings.manage'));
	let run = $derived(st?.run);
	let active = $derived(running(run));
	let avail = $derived(selectable(st));
	let withUpdate = $derived((st?.components ?? []).filter((c) => c.update));
	let current = $derived((st?.components ?? []).filter((c) => !c.update));
	// Nothing about "up to date" until a check has finished.
	let phase = $derived(checkPhase(st, checking));
	let checkErr = $derived(checkError(st));
	let quietCount = $derived(avail.filter((c) => !c.impact.disruptive).length);
	let pickedIds = $derived(avail.filter((c) => picked[c.id]).map((c) => c.id));

	async function load(force = true) {
		try {
			await updates.load(force);
			error = updates.error;
			lost = false;
		} catch (e) {
			error = errorText(e);
		}
	}

	async function checkNow() {
		checking = true;
		try {
			updates.set(await api('POST', '/updates/check'));
			error = '';
		} catch (e) {
			error = errorText(e);
		} finally {
			checking = false;
		}
	}

	// While a run is going: poll every second. The panel itself may restart
	// (it updates itself last): keep trying, then reload into the new version.
	onMount(() => {
		void load(false);
		const tick = setInterval(() => (now = Date.now()), 5000);
		let t: ReturnType<typeof setTimeout>;
		const poll = async () => {
			const runPoll = running(updates.status?.run) || restarting;
			if (runPoll) {
				try {
					const was = updates.status?.run?.state;
					const s = await api<import('$lib/updates').UpdatesStatus>('GET', '/updates');
					if ((restarting || was === 'restarting') && !running(s.run)) {
						location.reload();
						return;
					}
					updates.set(s);
					restarting = s.run?.state === 'restarting';
					// Done: the panel re-checks what's installed; read that too.
					if (!running(s.run)) setTimeout(() => void load(true), 1500);
					lost = false;
				} catch (e) {
					// The panel is down while it restarts: say so and keep trying.
					if (!(e instanceof ApiError) || e.status === 0 || e.status >= 500) lost = true;
				}
			} else if (checkPending(updates.status) && !checking) {
				// A check is running (or due: the panel checks a few seconds
				// after it starts): show its result as soon as it's in.
				try {
					updates.set(await api<import('$lib/updates').UpdatesStatus>('GET', '/updates'));
					error = '';
					lost = false;
				} catch (e) {
					if (!(e instanceof ApiError) || e.status === 0 || e.status >= 500) lost = true;
					else error = errorText(e);
				}
			}
			t = setTimeout(poll, lost ? 2000 : runPoll ? 1000 : 1500);
		};
		t = setTimeout(poll, 1000);
		return () => {
			clearInterval(tick);
			clearTimeout(t);
		};
	});

	async function plan(m: ApplyMode) {
		mode = m;
		ack = false;
		busy = true;
		try {
			preview = await api<Preview>('POST', '/updates/plan', {
				mode: m,
				components: m === 'selected' ? pickedIds : []
			});
			dialog?.open();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			busy = false;
		}
	}

	async function apply() {
		if (!preview) return;
		busy = true;
		try {
			await api<Run>('POST', '/updates/apply', {
				mode,
				components: mode === 'selected' ? pickedIds : [],
				acknowledge_breaking: ack
			});
			dialog?.close();
			picked = {};
			await load(true);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			busy = false;
		}
	}

	function openRollback() {
		restoreData = (run?.migrations_ran?.length ?? 0) > 0;
		rbDialog?.open();
	}

	async function rollback() {
		if (!run) return;
		busy = true;
		try {
			await api<Run>('POST', '/updates/rollback', { run_id: run.id, restore_data: restoreData });
			rbDialog?.close();
			await load(true);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		} finally {
			busy = false;
		}
	}

	function breakingOf(c: ComponentStatus) {
		return c.releases.filter((r) => r.breaking?.length);
	}
	let previewBreaking = $derived(
		preview ? withUpdate.filter((c) => preview!.items.some((i) => i.component === c.id) && c.breaking) : []
	);

	const stepIcon: Record<string, string> = {
		pending: '○',
		running: '◐',
		ok: '✓',
		failed: '✕',
		skipped: '–'
	};
	function compName(id: string) {
		return st?.components.find((c) => c.id === id)?.name ?? id;
	}
</script>

<svelte:head><title>Updates · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Updates</h1>
		<p aria-live="polite">
			{#if !st}Checking…{:else if active}An update is running.{:else if st.available}
				{st.available}
				{st.available === 1 ? 'part of this install has' : 'parts of this install have'} a newer release.
			{:else if phase === 'failed'}The last check didn’t finish.{:else if phase === 'done'}Everything is up to
				date.{:else if phase === 'checking'}Checking for updates…{:else}Not checked yet.{/if}
		</p>
	</div>
	<div class="actions">
		{#if st?.checked_at}
			<span class="muted checked" title={datetime(st.checked_at)}
				>Checked {relative(st.checked_at, now)}{#if st.next_check_at}, next {relative(
						st.next_check_at,
						now
					)}{/if}</span
			>
		{/if}
		<button class="btn btn-small" onclick={checkNow} disabled={checking || st?.checking}
			>{checking || st?.checking ? 'Checking…' : 'Check now'}</button
		>
	</div>
</div>

{#if error}<Notice>{error}</Notice>{/if}
{#if st?.error && phase !== 'failed'}<div class="gap"><Notice tone="warn">{st.error}</Notice></div>{/if}
{#if st?.rate_limit && st.rate_limit.remaining === 0}
	<div class="gap">
		<Notice tone="warn">
			GitHub’s API limit is used up until {datetime(st.rate_limit.reset)}; the last answers are shown.
			{#if !st.rate_limit.authenticated}Set <code>UPDATES_GITHUB_TOKEN</code> for 5,000 requests an hour.{/if}
		</Notice>
	</div>
{/if}

{#if lost}
	<div class="gap">
		<Notice tone="info"
			>{restarting || run?.state === 'restarting'
				? 'The panel is restarting into its new version. Reconnecting…'
				: 'The panel isn’t answering. Retrying…'}</Notice
		>
	</div>
{/if}

{#if run}
	<section class="section run" aria-labelledby="run-h">
		<div class="section-head">
			<h2 id="run-h">
				{run.kind === 'rollback' ? 'Rollback' : 'Update'}
				<span class="tag {stateTag[run.state]}">{stateWord[run.state]}</span>
			</h2>
			<p>
				{run.actor} started it
				<time datetime={run.started_at} title={datetime(run.started_at)}>{relative(run.started_at, now)}</time
				>
			</p>
		</div>
		<div class="card">
			<ul class="items">
				{#each run.items as it (it.component)}
					<li>
						<strong>{it.name}</strong>
						<span class="mono"
							>{run.kind === 'rollback' ? it.to : it.from} → {run.kind === 'rollback' ? it.from : it.to}</span
						>
					</li>
				{/each}
			</ul>
			<p class="summary-line">{run.summary}</p>
			{#if run.error}<Notice>{run.error}</Notice>{/if}

			<ol class="steps" aria-label="Progress">
				{#each run.steps as s (s.id)}
					<li class="step {s.state}">
						<span class="icon" aria-hidden="true">{stepIcon[s.state]}</span>
						<span class="label">{s.label}<span class="sr-only">: {s.state}</span></span>
						{#if s.detail}<span class="detail">{s.detail}</span>{/if}
					</li>
				{/each}
			</ol>

			{#if run.backup}
				<div class="backup">
					<h3>Backup</h3>
					<p>
						<span class="mono">{bytes(run.backup.bytes)}</span> in
						<code>{run.backup.dir}</code>{#if run.backup.free_bytes}, {bytes(run.backup.free_bytes)} was free{/if}.
						{run.backup.files.length} files: {run.backup.files.map((f) => f.name).join(', ')}.
						{#if run.backup.pruned?.length}Removed older backups: {run.backup.pruned.join(', ')}.{/if}
					</p>
				</div>
			{/if}

			{#if run.checks.length}
				<div class="checks">
					<h3>Checks</h3>
					<ul>
						{#each run.checks as c (c.id)}
							<li class:ok={c.ok} class:bad={!c.ok && !c.skipped}>
								<span class="icon" aria-hidden="true">{c.ok ? '✓' : c.skipped ? '–' : '✕'}</span>
								<span>{c.label}<span class="sr-only">: {c.ok ? 'passed' : 'failed'}</span></span>
								{#if c.detail}<span class="detail">{c.detail}</span>{/if}
							</li>
						{/each}
					</ul>
				</div>
			{/if}

			{#if run.migrations_ran?.length}
				<Notice tone="warn"
					>This {run.kind} ran database migrations in <strong>{run.migrations_ran.join(', ')}</strong>.
					{#if run.kind === 'update'}A rollback restores those databases from the backup, so changes made
						since the update are lost.{/if}</Notice
				>
			{/if}

			{#if run.log.length}
				<details class="log">
					<summary>Helper output ({run.log.length} lines)</summary>
					<pre>{run.log.join('\n')}</pre>
				</details>
			{/if}

			{#if run.can_rollback && !active && canApply && run.kind === 'update'}
				<div class="rb">
					<p>
						{run.state === 'failed'
							? 'Something failed. Put back exactly what ran before:'
							: 'Not happy with it? Put back exactly what ran before:'}
					</p>
					<button class="btn {run.state === 'failed' ? 'btn-primary' : ''}" onclick={openRollback}
						>Roll back</button
					>
				</div>
			{/if}
		</div>
	</section>
{/if}

{#if !st}
	<State kind="loading" />
{:else}
	{#if !st.apply.enabled}
		<div class="gap"><Notice tone="info">{st.apply.why}</Notice></div>
	{/if}

	<section class="section" aria-labelledby="avail-h">
		<div class="section-head">
			<h2 id="avail-h">Available</h2>
			{#if st.apply.enabled}
				<p>
					Every update backs up first ({st.apply.backup_root
						? st.apply.backup_root
						: 'backups/updates in the stack directory'}, the last {st.apply.keep} kept).
				</p>
			{/if}
		</div>
		{#if phase === 'checking' || phase === 'waiting'}
			<State
				kind="loading"
				title={phase === 'checking' ? 'Checking for updates…' : 'Waiting for the first check…'}
				>{phase === 'checking'
					? 'Asking GitHub and the image registry for the newest releases.'
					: 'The panel checks a few seconds after it starts.'} The results show here when it finishes.</State
			>
		{:else if phase === 'failed'}
			<State kind="error" title="Couldn’t check for updates">
				<p>{checkErr}</p>
				{#snippet action()}
					<button class="btn btn-small" onclick={checkNow} disabled={checking || st.checking}
						>{checking || st.checking ? 'Checking…' : 'Try again'}</button
					>
				{/snippet}
			</State>
		{:else if !withUpdate.length}
			<State kind="empty" title="Nothing to update">Every part of this install runs its newest release.</State
			>
		{:else}
			<div class="list">
				{#each withUpdate as c (c.id)}
					{@const iw = impactWord(c.impact)}
					<article class="comp" class:breaking={c.breaking} aria-labelledby="c-{c.id}">
						<header>
							{#if c.applyable && st.apply.enabled && canApply}
								<input
									type="checkbox"
									id="pick-{c.id}"
									bind:checked={picked[c.id]}
									disabled={active}
									aria-label="Select {c.name}"
								/>
							{/if}
							<div class="title">
								<h3 id="c-{c.id}">
									<label for="pick-{c.id}">{c.name}</label>
									<span class="about">{c.about}</span>
								</h3>
								<p class="ver mono">
									{c.installed} <span aria-hidden="true">→</span><span class="sr-only">to</span>
									<strong>{c.latest}</strong>
									<span class="muted">· {behind(c)}</span>
								</p>
							</div>
							<div class="tags">
								{#if c.breaking}<span class="tag tag-bad">Breaking</span>{/if}
								<span class="tag {iw.tone}">{iw.text}</span>
							</div>
						</header>
						<p class="who">
							<strong>Who is affected:</strong>
							{c.impact.who}
							{#if c.impact.restart}Restarts {c.services.join(', ')}.{/if}
							{#if c.never_restart?.length}Never touches {c.never_restart.join(', ')}.{/if}
						</p>
						{#if c.breaking}
							<Notice tone="bad">
								<strong>Needs attention before updating:</strong>
								{breakingOf(c)
									.map((r) => `${r.version} (${r.breaking?.join(', ')})`)
									.join('; ')}
								{#if c.breaking_notes?.length}
									<ul>
										<!-- eslint-disable-next-line svelte/no-at-html-tags -->
										{#each c.breaking_notes as n, i (i)}<li>{@html renderInline(n)}</li>{/each}
									</ul>
								{/if}
							</Notice>
						{/if}
						{#if !c.applyable && c.why}<p class="muted why">{c.why}</p>{/if}
						{#if c.pending?.length}<p class="muted why">
								{c.pending.join(', ')} released, image not published yet.
							</p>{/if}
						<details class="changelog">
							<summary
								>Changelog: {c.releases.length}
								{c.releases.length === 1 ? 'release' : 'releases'}
								<span class="muted">(github.com/{c.repo})</span></summary
							>
							{#each c.releases as r (r.tag)}
								<div class="release" class:is-breaking={r.breaking?.length}>
									<div class="rhead">
										<strong class="mono">{r.version}</strong>
										{#each r.breaking ?? [] as b (b)}<span class="tag tag-bad">{b}</span>{/each}
										{#if r.published_at}<span class="muted">{datetime(r.published_at)}</span>{/if}
										{#if r.url}<a href={r.url} target="_blank" rel="noopener noreferrer">on GitHub</a>{/if}
									</div>
									<!-- Sanitised: renderMarkdown escapes every character of the body first. -->
									<!-- eslint-disable-next-line svelte/no-at-html-tags -->
									<div class="md">{@html renderMarkdown(withoutTitle(r.body, r.version))}</div>
								</div>
							{/each}
						</details>
					</article>
				{/each}
			</div>
			{#if st.apply.enabled && canApply && avail.length}
				<div class="applybar">
					<button class="btn btn-primary" disabled={active || busy} onclick={() => plan('all')}
						>Update all ({avail.length})</button
					>
					<button class="btn" disabled={active || busy || !pickedIds.length} onclick={() => plan('selected')}
						>Update selected{pickedIds.length ? ` (${pickedIds.length})` : ''}</button
					>
					{#if quietCount && quietCount < avail.length}
						<button class="btn" disabled={active || busy} onclick={() => plan('quiet')}
							>Without disconnecting users ({quietCount})</button
						>
					{/if}
				</div>
			{:else if st.apply.enabled && !canApply}
				<p class="muted">Applying updates also needs <code>settings.manage</code>.</p>
			{/if}
		{/if}
	</section>

	{#if phase !== 'checking' && phase !== 'waiting' && current.length}
		<section class="section" aria-labelledby="cur-h">
			<div class="section-head"><h2 id="cur-h">Up to date</h2></div>
			<div class="table-wrap uptodate">
				<table>
					<thead>
						<tr><th>Part</th><th>Installed</th><th>Newest</th><th>Note</th></tr>
					</thead>
					<tbody>
						{#each current as c (c.id)}
							<tr>
								<td>{c.name} <span class="muted about-cell">{c.about}</span></td>
								<td class="mono">{c.present ? c.installed || '—' : 'not installed'}</td>
								<td class="mono">{c.latest || '—'}</td>
								<td class="muted"
									>{c.error ?? (c.pending?.length ? `${c.pending.join(', ')} not published yet` : '')}</td
								>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</section>
	{/if}

	{#if st.history.length}
		<section class="section" aria-labelledby="hist-h">
			<div class="section-head"><h2 id="hist-h">History</h2></div>
			<div class="table-wrap">
				<table>
					<thead><tr><th>When</th><th>Who</th><th>What</th><th>Result</th></tr></thead>
					<tbody>
						{#each st.history as h (h.id)}
							<tr>
								<td title={datetime(h.started_at)}>{relative(h.started_at, now)}</td>
								<td>{h.actor}</td>
								<td>
									{h.kind === 'rollback' ? 'Rolled back ' : ''}{h.items
										.map(
											(i) =>
												`${i.name} ${h.kind === 'rollback' ? i.to + ' → ' + i.from : i.from + ' → ' + i.to}`
										)
										.join(', ')}
								</td>
								<td><span class="tag {stateTag[h.state]}">{stateWord[h.state]}</span></td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</section>
	{/if}
{/if}

<Dialog
	bind:this={dialog}
	title="Apply {preview?.items.length ?? 0} {preview?.items.length === 1 ? 'update' : 'updates'}"
>
	{#if preview}
		<p class="impact-line">{preview.summary}</p>
		<ul class="plan">
			{#each preview.items as it (it.component)}
				{@const im = preview.impacts.find((x) => x.component === it.component)}
				<li>
					<div><strong>{it.name}</strong> <span class="mono">{it.from} → {it.to}</span></div>
					{#if im}<div class="muted">{im.impact.who}</div>{/if}
				</li>
			{/each}
		</ul>
		{#if preview.skipped.length}
			<p class="muted">Left out (they disconnect users): {preview.skipped.map(compName).join(', ')}.</p>
		{/if}
		{#if preview.breaking}
			<Notice tone="bad">
				<strong>Breaking changes.</strong>
				{#each previewBreaking as c (c.id)}
					<div class="bk">
						{c.name}: {breakingOf(c)
							.map((r) => `${r.version} (${r.breaking?.join(', ')})`)
							.join('; ')}
						{#if c.breaking_notes?.length}
							<ul>
								<!-- eslint-disable-next-line svelte/no-at-html-tags -->
								{#each c.breaking_notes as n, i (i)}<li>{@html renderInline(n)}</li>{/each}
							</ul>
						{/if}
					</div>
				{/each}
				<label class="ack"
					><input type="checkbox" bind:checked={ack} /> I’ve read these and done any manual steps they need</label
				>
			</Notice>
		{/if}
		<p class="muted small">
			First a backup (.env, config, image digests, every database, data volumes); then the new versions are
			pulled and only these services are recreated. If a check fails you can roll back.
		</p>
		<div class="dlg-actions">
			<button class="btn" onclick={() => dialog?.close()}>Cancel</button>
			<button class="btn btn-primary" disabled={busy || (preview.breaking && !ack)} onclick={apply}
				>{busy ? 'Starting…' : 'Back up and update'}</button
			>
		</div>
	{/if}
</Dialog>

<Dialog bind:this={rbDialog} title="Roll back">
	{#if run}
		<p>
			Puts back the exact image digests that ran before, the <code>.env</code> and config from the backup, then
			runs the same checks again.
		</p>
		<ul class="plan">
			{#each run.items as it (it.component)}
				<li><strong>{it.name}</strong> <span class="mono">{it.to} → {it.from}</span></li>
			{/each}
		</ul>
		<label class="ack">
			<input type="checkbox" bind:checked={restoreData} />
			Restore the data too (databases and volumes from {run.backup?.dir})
		</label>
		{#if run.migrations_ran?.length}
			<Notice tone="warn"
				>The update migrated {run.migrations_ran.join(', ')}: the previous version may not work with the new
				schema. Keep this on. Changes made since the update are lost.</Notice
			>
		{/if}
		<div class="dlg-actions">
			<button class="btn" onclick={() => rbDialog?.close()}>Cancel</button>
			<button class="btn btn-danger" disabled={busy} onclick={rollback}
				>{busy ? 'Starting…' : 'Roll back'}</button
			>
		</div>
	{/if}
</Dialog>

<style>
	.actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3);
	}
	.checked {
		font-size: var(--text-sm);
	}
	.gap {
		margin-top: var(--space-3);
		max-width: 760px;
	}
	.card,
	.comp {
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
		padding: var(--space-4) var(--space-5);
	}
	.run h2 {
		display: flex;
		align-items: center;
		gap: var(--space-3);
	}
	.items {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-5);
		list-style: none;
		padding: 0;
		margin: 0 0 var(--space-2);
	}
	.items li {
		display: flex;
		gap: var(--space-2);
		align-items: baseline;
	}
	.summary-line,
	.impact-line {
		font-weight: 600;
		margin-bottom: var(--space-3);
	}
	.steps,
	.checks ul {
		list-style: none;
		padding: 0;
		margin: var(--space-3) 0;
		display: grid;
		gap: var(--space-2);
	}
	.step,
	.checks li {
		display: grid;
		grid-template-columns: 20px minmax(0, 1fr);
		column-gap: var(--space-2);
		font-size: var(--text-sm);
	}
	.icon {
		font-family: var(--font-mono);
		color: var(--fg-quiet);
		text-align: center;
	}
	.detail {
		grid-column: 2;
		color: var(--fg-quiet);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		overflow-wrap: anywhere;
	}
	.step.ok .icon,
	.checks li.ok .icon {
		color: var(--success-text);
	}
	.step.failed .icon,
	.checks li.bad .icon,
	.checks li.bad {
		color: var(--danger-text);
	}
	.step.running .icon {
		color: var(--accent-text);
		animation: spin 1.2s linear infinite;
	}
	.step.running .label {
		color: var(--accent-text);
	}
	.step.pending,
	.step.skipped {
		color: var(--fg-quiet);
	}
	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
	h3 {
		font-size: var(--text-sm);
		font-family: var(--font-display);
		margin: var(--space-4) 0 var(--space-1);
	}
	.backup p {
		font-size: var(--text-sm);
		color: var(--fg-2);
		overflow-wrap: anywhere;
	}
	.log pre {
		max-height: 280px;
		overflow: auto;
		font-size: var(--text-xs);
		background: var(--bg);
		padding: var(--space-3);
		border: 1px solid var(--border-soft);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.log summary,
	.changelog summary {
		cursor: pointer;
		font-size: var(--text-sm);
		margin-top: var(--space-3);
	}
	.rb {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
		margin-top: var(--space-4);
		padding-top: var(--space-4);
		border-top: 1px solid var(--border-soft);
	}
	.list {
		display: grid;
		gap: var(--space-3);
	}
	.comp.breaking {
		border-color: color-mix(in oklab, var(--danger), var(--border) 55%);
	}
	.comp header {
		display: flex;
		align-items: flex-start;
		gap: var(--space-3);
	}
	.comp header input {
		margin-top: 4px;
		accent-color: var(--accent);
		width: 16px;
		height: 16px;
	}
	.title {
		flex: 1;
		min-width: 0;
	}
	.comp h3 {
		margin: 0;
		font-size: var(--text-base);
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: baseline;
	}
	.about {
		font-family: var(--font-body);
		font-weight: 400;
		font-size: var(--text-sm);
		color: var(--fg-quiet);
	}
	.ver {
		font-size: var(--text-sm);
		margin-top: 2px;
	}
	.tags {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
		justify-content: flex-end;
	}
	.who {
		font-size: var(--text-sm);
		color: var(--fg-2);
		margin: var(--space-2) 0;
	}
	.why {
		font-size: var(--text-sm);
		margin: var(--space-2) 0 0;
	}
	.release {
		border-top: 1px solid var(--border-soft);
		padding: var(--space-3) 0;
	}
	.release.is-breaking {
		background: color-mix(in oklab, var(--danger), transparent 95%);
		padding-inline: var(--space-3);
		border-radius: var(--radius-sm);
	}
	.rhead {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		font-size: var(--text-sm);
	}
	.md {
		font-size: var(--text-sm);
		color: var(--fg-2);
		overflow-wrap: anywhere;
	}
	.md :global(h3),
	.md :global(h4),
	.md :global(h5),
	.md :global(h6) {
		font-size: var(--text-sm);
		color: var(--fg);
		margin: var(--space-2) 0 var(--space-1);
	}
	.md :global(ul),
	.md :global(ol) {
		padding-left: var(--space-5);
		margin: var(--space-1) 0;
	}
	.md :global(code) {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}
	.applybar {
		position: sticky;
		bottom: 0;
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		padding: var(--space-3) 0;
		margin-top: var(--space-3);
		background: var(--bg);
		border-top: 1px solid var(--border-soft);
	}
	.plan {
		list-style: none;
		padding: 0;
		margin: var(--space-3) 0;
		display: grid;
		gap: var(--space-2);
		font-size: var(--text-sm);
	}
	.bk {
		margin-top: var(--space-2);
	}
	.ack {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
		margin-top: var(--space-3);
		font-size: var(--text-sm);
		color: var(--fg);
	}
	.small {
		font-size: var(--text-xs);
		margin-top: var(--space-3);
	}
	.dlg-actions {
		display: flex;
		justify-content: flex-end;
		gap: var(--space-2);
		margin-top: var(--space-4);
	}
	@media (max-width: 640px) {
		.comp {
			padding: var(--space-3);
		}
		.comp header {
			flex-wrap: wrap;
		}
		.tags {
			width: 100%;
			justify-content: flex-start;
			padding-left: 28px;
		}
		.applybar .btn {
			flex: 1 1 100%;
		}
		.uptodate .about-cell {
			display: none;
		}
	}
</style>
