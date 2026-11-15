<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { stackConfig } from '$lib/stackconfig.svelte';
	import type { ConfigServiceStatus } from '$lib/types';
	import {
		DEFAULTS,
		DOCS_URL,
		KEYS,
		changeCount,
		changes,
		draftFrom,
		humanDuration,
		humanSize,
		parseDuration,
		parseSize,
		storageState,
		usedShare,
		validate,
		type Draft,
		type Driver,
		type Field,
		type FilesView,
		type Source
	} from '$lib/files';
	import ChoiceGroup, { type Choice } from '$lib/components/ChoiceGroup.svelte';
	import ConfigStatus from '$lib/components/ConfigStatus.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';
	import Toggle from '$lib/components/Toggle.svelte';

	let view = $derived(stackConfig.view);
	let files = $state<FilesView | null>(null);
	let loaded = $state(false);
	let refreshing = $state(false);
	let saving = $state(false);
	let checking = $state(false);
	let serverError = $state<{ field: Field; message: string } | null>(null);

	let saved = $state<Draft | null>(null);
	let sources = $state<Record<Field, Source> | null>(null);
	let draft = $state<Draft | null>(null);

	let status = $derived(files?.status ?? null);
	let editable = $derived(!!view?.keys.find((k) => k.key === KEYS.enabled)?.editable);
	let configOff = $derived(session.info?.features.config_file === false || view?.enabled === false);
	let errors = $derived(draft ? validate(draft) : {});
	let shownErrors = $derived.by(() => {
		const out: Partial<Record<Field, string>> = { ...errors };
		if (serverError && !out[serverError.field]) out[serverError.field] = serverError.message;
		return out;
	});
	let hasErrors = $derived(Object.keys(errors).length > 0);
	let change = $derived(draft && saved ? changes(draft, saved) : {});
	let count = $derived(changeCount(change));
	let dirty = $derived(count > 0);
	let storage = $derived(storageState(status));
	let filesStatus = $derived<ConfigServiceStatus[]>(
		(stackConfig.statuses ?? []).filter((s) => s.name === 'files')
	);
	let canModerate = $derived(session.can('users.moderate') || session.can('users.manage'));

	const fieldKey = Object.fromEntries(Object.entries(KEYS).map(([f, k]) => [k, f])) as Record<string, Field>;

	function fill() {
		const r = draftFrom(view, status);
		saved = r.draft;
		sources = r.sources;
		draft = { ...r.draft };
		serverError = null;
	}

	async function loadStatus() {
		try {
			files = await api<FilesView>('GET', '/files/status');
		} catch (e) {
			files = { configured: true, keys_env: [], error: errorText(e), checked_at: new Date().toISOString() };
		}
	}

	async function load() {
		await Promise.all([stackConfig.load(), loadStatus()]);
		fill();
		loaded = true;
	}

	async function refresh() {
		refreshing = true;
		try {
			await loadStatus();
			if (!dirty) fill();
		} finally {
			refreshing = false;
		}
	}

	onMount(() => {
		load();
		const poll = setInterval(() => {
			if (document.visibilityState === 'visible' && loaded && !refreshing) refresh();
		}, 15000);
		return () => clearInterval(poll);
	});

	async function recheck() {
		checking = true;
		try {
			await stackConfig.checkServices();
		} catch {
			/* the Stack config page reports it */
		} finally {
			checking = false;
		}
	}
	async function follow() {
		for (const wait of [800, 2000, 4000]) {
			await new Promise((r) => setTimeout(r, wait));
			await recheck();
			if (!stackConfig.statuses?.some((s) => s.name === 'files' && s.state === 'pending')) break;
		}
		await refresh();
	}

	async function save(e: Event) {
		e.preventDefault();
		if (!dirty || hasErrors || !draft) return;
		saving = true;
		serverError = null;
		try {
			await stackConfig.save(change);
			fill();
			toasts.push('Uploads saved. The files service picks it up within seconds.');
			follow();
		} catch (err) {
			if (err instanceof ApiError && err.field && fieldKey[err.field]) {
				serverError = { field: fieldKey[err.field], message: err.message };
			}
			toasts.push(errorText(err), 'bad');
			if (err instanceof Error && /changed since you loaded/.test(err.message)) await stackConfig.load();
		} finally {
			saving = false;
		}
	}
	function discard() {
		if (saved) draft = { ...saved };
		serverError = null;
	}

	const drivers: Choice<Driver>[] = [
		{
			value: 'local',
			label: 'Local disk',
			detail: 'The files_data volume on this server. Simplest; the disk’s free space bounds the total.',
			note: 'Default'
		},
		{
			value: 's3',
			label: 'S3-compatible',
			detail: 'MinIO (the stack’s minio profile), Cloudflare R2, AWS S3 or anything that speaks S3.'
		}
	];

	function setText(f: Field, v: string) {
		if (draft) (draft as unknown as Record<string, unknown>)[f] = v;
	}
	const errId = (f: Field) => `fl-${f}-err`;
	/** What a size or time field means, under it. */
	function sizeHint(v: string, f: Field) {
		const n = parseSize(v || String(DEFAULTS[f as keyof typeof DEFAULTS]));
		return n === null ? '' : humanSize(n);
	}
	function durHint(v: string, f: Field) {
		const n = parseDuration(v || String(DEFAULTS[f as keyof typeof DEFAULTS]));
		return n === null ? '' : humanDuration(n);
	}
	function from(f: Field) {
		if (!sources || !draft || !saved || draft[f] !== saved[f]) return '';
		return sources[f] === 'service'
			? 'From the files service’s env.'
			: sources[f] === 'default'
				? 'Default.'
				: '';
	}
</script>

<svelte:head><title>Uploads · boson admin</title></svelte:head>

{#snippet sizeField(f: Field, id: string, label: string, hint: string, placeholder: string)}
	{#if draft}
		<div class="field">
			<label class="lbl" for={id}>{label}</label>
			<input
				{id}
				type="text"
				inputmode="decimal"
				bind:value={() => draft![f] as string, (v: string) => setText(f, v)}
				{placeholder}
				autocomplete="off"
				spellcheck="false"
				aria-invalid={!!shownErrors[f]}
				aria-describedby="{id}-hint{shownErrors[f] ? ' ' + errId(f) : ''}"
			/>
			{#if shownErrors[f]}<p class="err" id={errId(f)}>{shownErrors[f]}</p>{/if}
			<small id="{id}-hint"
				>{#if !shownErrors[f]}<span class="means">= {sizeHint(draft[f] as string, f)}</span>{/if}
				{hint}
				{from(f)}</small
			>
		</div>
	{/if}
{/snippet}

{#snippet durField(f: Field, id: string, label: string, hint: string)}
	{#if draft}
		<div class="field">
			<label class="lbl" for={id}>{label}</label>
			<input
				{id}
				type="text"
				bind:value={() => draft![f] as string, (v: string) => setText(f, v)}
				placeholder="72h"
				autocomplete="off"
				spellcheck="false"
				aria-invalid={!!shownErrors[f]}
				aria-describedby="{id}-hint{shownErrors[f] ? ' ' + errId(f) : ''}"
			/>
			{#if shownErrors[f]}<p class="err" id={errId(f)}>{shownErrors[f]}</p>{/if}
			<small id="{id}-hint"
				>{#if !shownErrors[f]}<span class="means">= {durHint(draft[f] as string, f)}</span>{/if}
				{hint}
				{from(f)}</small
			>
		</div>
	{/if}
{/snippet}

<div class="page-head">
	<div>
		<h1>Uploads</h1>
		<p>
			Files people attach in the boson app, served from their own host. Every upload counts against a rolling
			quota until it leaves the window, even after it’s deleted; files themselves are removed when they
			expire.
		</p>
		<p class="docs">
			<a href={DOCS_URL} target="_blank" rel="noreferrer">Setup guide: docs/files.md</a>
			{#if canModerate}<a class="plain" href="/files">Uploaded files →</a>{/if}
		</p>
	</div>
</div>

{#if configOff}
	<State kind="off" title="Uploads live in the stack config file">
		Set <code>BOSON_CONFIG=/config/boson.yaml</code> on the admin service and mount the stack’s
		<code>config/</code> directory read-write. The files service reads the same file’s <code>files:</code> section.
	</State>
{:else if !loaded}
	<State kind="loading" title="Loading uploads…" />
{:else if stackConfig.error && !view}
	<State kind="error">
		{stackConfig.error}
		{#snippet action()}<button class="btn btn-small" onclick={load}>Try again</button>{/snippet}
	</State>
{:else if draft}
	<div class="notes">
		{#if view?.error}
			<Notice tone="bad">
				The config file has an error (line {view.error.line ?? '?'}): {view.error.message}. Fix it on the
				<a href="/config">Stack config</a> page before saving.
			</Notice>
		{/if}
		{#if files && !files.configured}
			<Notice tone="warn">
				<strong>The files service isn’t reachable from the admin panel.</strong> You can still save these
				settings, but there’s no live status or moderation. Set
				<code>BOSON_FILES_INTERNAL_URL=http://files:3001</code> and <code>BOSON_FILES_INTERNAL_TOKEN</code> (the
				files service’s internal token) on the admin service.
			</Notice>
		{:else if files?.error}
			<Notice tone="bad">Couldn’t read the files service’s status: {files.error}</Notice>
		{/if}
		{#if !editable}
			<Notice tone="info"
				>You can look but not change these: saving needs <code>settings.manage</code>.</Notice
			>
		{/if}
	</div>

	<div class="layout">
		<form class="form" onsubmit={save} novalidate>
			<fieldset disabled={!editable}>
				<legend class="sr-only">Upload settings</legend>
				<section class="card switch" class:on={draft.enabled} aria-labelledby="fl-on-h">
					<h2 id="fl-on-h" class="sr-only">Switch</h2>
					<Toggle
						label="Let people upload files"
						detail="Shows the attach button in the boson app (auth’s /v1/config gets files_url). Off: uploads are refused; files already shared keep working until they expire."
						bind:checked={draft.enabled}
					/>
				</section>

				<section class="group" aria-labelledby="fl-time-h">
					<div class="group-head">
						<h2 id="fl-time-h">How long</h2>
						<p>
							Times like <code>72h</code>, <code>3d</code> or <code>90m</code>. Both default to 3 days.
						</p>
					</div>
					<div class="card fields row2">
						{@render durField(
							'retention',
							'fl-retention',
							'Keep files for',
							'Then the file is deleted and its link stops working.'
						)}
						{@render durField(
							'window',
							'fl-window',
							'Quota window',
							'Uploads count against the quota for this long after they’re made.'
						)}
					</div>
				</section>

				<section class="group" aria-labelledby="fl-acc-h">
					<div class="group-head">
						<h2 id="fl-acc-h">Signed-in people</h2>
						<p>
							Per account, over the quota window. Sizes like <code>5GB</code> or <code>100 MB</code> (1 GB = 1024
							MB).
						</p>
					</div>
					<div class="card fields row2">
						{@render sizeField('account_quota', 'fl-acc-quota', 'Quota', 'Per window.', '5GB')}
						{@render sizeField('account_max_file', 'fl-acc-max', 'Largest file', 'Per upload.', '100MB')}
					</div>
				</section>

				<section class="group" aria-labelledby="fl-guest-h">
					<div class="group-head">
						<h2 id="fl-guest-h">Guests</h2>
						<p>
							People on IRC without an account. They get a short-lived guest token; one address can’t hold
							more than three guests’ quota.
						</p>
					</div>
					<div class="card fields">
						<Toggle
							label="Guests can upload"
							detail="Off: only signed-in people can attach files."
							bind:checked={draft.guest_enabled}
						/>
						<div class="row2" class:dim={!draft.guest_enabled}>
							{@render sizeField(
								'guest_quota',
								'fl-g-quota',
								'Guest quota',
								'Per guest, per window.',
								'500MB'
							)}
							{@render sizeField('guest_max_file', 'fl-g-max', 'Largest guest file', 'Per upload.', '25MB')}
						</div>
					</div>
				</section>

				<section class="group" aria-labelledby="fl-st-h">
					<div class="group-head">
						<h2 id="fl-st-h">Storage</h2>
						<p>Where the files are kept. Changing it doesn’t move files already uploaded.</p>
					</div>
					<div class="card fields">
						<ChoiceGroup legend="Driver" name="driver" choices={drivers} bind:value={draft.driver} />
						{#if draft.driver === 's3'}
							<div class="s3" data-testid="s3-fields">
								<div class="field">
									<label class="lbl" for="fl-ep">Endpoint</label>
									<input
										id="fl-ep"
										type="text"
										inputmode="url"
										bind:value={draft.s3_endpoint}
										placeholder="http://minio:9000"
										autocomplete="off"
										spellcheck="false"
										aria-invalid={!!shownErrors.s3_endpoint}
										aria-describedby="fl-ep-hint{shownErrors.s3_endpoint ? ' ' + errId('s3_endpoint') : ''}"
									/>
									{#if shownErrors.s3_endpoint}<p class="err" id={errId('s3_endpoint')}>
											{shownErrors.s3_endpoint}
										</p>{/if}
									<small id="fl-ep-hint"
										><code>http://minio:9000</code> for the stack’s minio profile, or
										<code>https://&lt;account&gt;.r2.cloudflarestorage.com</code> for R2.</small
									>
								</div>
								<div class="row2">
									<div class="field">
										<label class="lbl" for="fl-bucket">Bucket</label>
										<input
											id="fl-bucket"
											type="text"
											bind:value={draft.s3_bucket}
											placeholder="boson-files"
											autocomplete="off"
											spellcheck="false"
											aria-invalid={!!shownErrors.s3_bucket}
											aria-describedby={shownErrors.s3_bucket ? errId('s3_bucket') : undefined}
										/>
										{#if shownErrors.s3_bucket}<p class="err" id={errId('s3_bucket')}>
												{shownErrors.s3_bucket}
											</p>{/if}
									</div>
									<div class="field">
										<label class="lbl" for="fl-region">Region</label>
										<input
											id="fl-region"
											type="text"
											bind:value={draft.s3_region}
											placeholder="auto"
											autocomplete="off"
											spellcheck="false"
											aria-invalid={!!shownErrors.s3_region}
											aria-describedby={shownErrors.s3_region ? errId('s3_region') : undefined}
										/>
										{#if shownErrors.s3_region}<p class="err" id={errId('s3_region')}>
												{shownErrors.s3_region}
											</p>{/if}
									</div>
								</div>
								<div class="tgl">
									<Toggle
										label="Path-style URLs"
										detail="endpoint/bucket/key instead of bucket.endpoint/key. MinIO needs it on."
										bind:checked={draft.s3_path_style}
									/>
								</div>
								<div class="field">
									<label class="lbl" for="fl-pub">Public base URL <span class="opt">optional</span></label>
									<input
										id="fl-pub"
										type="text"
										inputmode="url"
										bind:value={draft.s3_public_base_url}
										placeholder="https://cdn.example.com"
										autocomplete="off"
										spellcheck="false"
										aria-invalid={!!shownErrors.s3_public_base_url}
										aria-describedby="fl-pub-hint{shownErrors.s3_public_base_url
											? ' ' + errId('s3_public_base_url')
											: ''}"
									/>
									{#if shownErrors.s3_public_base_url}<p class="err" id={errId('s3_public_base_url')}>
											{shownErrors.s3_public_base_url}
										</p>{/if}
									<small id="fl-pub-hint"
										>Serve files straight from the bucket or a CDN. Empty (recommended): through the files
										host, so its safety headers always apply.</small
									>
								</div>
								<div class="field">
									<span id="fl-keys-l">Access keys</span>
									<div class="keys" aria-labelledby="fl-keys-l">
										{#each files?.keys_env?.length ? files.keys_env : ['FILES_S3_ACCESS_KEY', 'FILES_S3_SECRET_KEY'] as k (k)}
											<code>{k}</code>
										{/each}
									</div>
									<small
										>Secrets, so they come from the stack’s <code>.env</code>, never this file. After changing
										them run <code>docker compose up -d files</code>.</small
									>
								</div>
							</div>
						{/if}
					</div>
				</section>
			</fieldset>

			<div class="bar" class:dirty>
				<p class="meta" aria-live="polite">
					{#if dirty && hasErrors}
						Fix the highlighted fields to save
					{:else if dirty}
						{count === 1 ? 'One unsaved change' : `${count} unsaved changes`}
					{:else if view?.saved_at}
						Config file saved <Time at={view.saved_at} />{#if view.saved_by}&nbsp;by {view.saved_by}{/if}
					{:else}
						Saved to <code>files:</code> in config/boson.yaml
					{/if}
				</p>
				<div class="btns">
					<button class="btn btn-ghost" type="button" disabled={!dirty || saving} onclick={discard}
						>Discard</button
					>
					<button class="btn btn-primary" disabled={!dirty || hasErrors || saving || !editable}
						>{saving ? 'Saving…' : 'Save changes'}</button
					>
				</div>
			</div>
		</form>

		<aside class="side" aria-labelledby="fl-status-h">
			<div class="card status" data-storage={storage}>
				<div class="st-head">
					<h2 id="fl-status-h">Status</h2>
					<button
						class="btn btn-small btn-ghost"
						type="button"
						onclick={refresh}
						disabled={refreshing || !files?.configured}>{refreshing ? 'Checking…' : 'Refresh'}</button
					>
				</div>
				{#if status}
					{@const st = status.storage}
					<dl>
						<dt>Uploads</dt>
						<dd>
							{#if status.config?.enabled}<span class="tag tag-accent">On</span>{:else}<span class="tag"
									>Off</span
								>{/if}
							{#if status.config && !status.config.guest_enabled}<span class="tag">No guests</span>{/if}
						</dd>
						<dt>Storage</dt>
						<dd>
							<span class="tag {st.ok ? 'tag-ok' : 'tag-bad'}" data-testid="storage-state"
								><span class="dot" aria-hidden="true"></span>{st.ok ? 'Healthy' : 'Failing'}</span
							>
							<span class="muted"
								>{st.driver === 's3' ? 'S3' : st.driver === 'local' ? 'Local disk' : st.driver}</span
							>
						</dd>
						{#if st.bucket}
							<dt>Bucket</dt>
							<dd class="wrap">{st.bucket}</dd>
						{/if}
						{#if st.free_bytes !== undefined && st.free_bytes !== null}
							<dt>Free space</dt>
							<dd>
								{humanSize(st.free_bytes)}{#if st.total_bytes}<span class="muted">
										of {humanSize(st.total_bytes)}</span
									>{/if}
							</dd>
						{/if}
						<dt>Live files</dt>
						<dd>{status.totals.files.toLocaleString()} · {humanSize(status.totals.bytes)}</dd>
						<dt>In the window</dt>
						<dd>
							{status.totals.window_uploads.toLocaleString()} uploads · {humanSize(
								status.totals.window_bytes
							)}
						</dd>
						<dt>Uploaders</dt>
						<dd>{status.totals.uploaders.toLocaleString()}</dd>
						{#if status.version}
							<dt>Version</dt>
							<dd>{status.version}</dd>
						{/if}
					</dl>
					{#if st.free_bytes && st.total_bytes}
						{@const used = usedShare(st.total_bytes - st.free_bytes, st.total_bytes)}
						<div
							class="meter"
							class:hot={used > 0.9}
							role="meter"
							aria-label="Disk used"
							aria-valuemin="0"
							aria-valuemax="100"
							aria-valuenow={Math.round(used * 100)}
						>
							<span style:width="{used * 100}%"></span>
						</div>
					{/if}
					{#if st.error || status.errors.length}
						<ul class="problems">
							{#if st.error}<li>{st.error}</li>{/if}
							{#each status.errors as e, i (i)}<li>{e}</li>{/each}
						</ul>
					{/if}
					{#if status.config}
						{@const c = status.config}
						<h3 class="sub">Running with</h3>
						<dl class="echo">
							<dt>Kept for</dt>
							<dd>{humanDuration(c.retention_seconds)}</dd>
							<dt>Accounts</dt>
							<dd>
								{humanSize(c.limits.account.quota_bytes)} / {humanDuration(c.limits.account.window_seconds)},
								≤ {humanSize(c.limits.account.max_file_bytes)} each
							</dd>
							<dt>Guests</dt>
							<dd>
								{humanSize(c.limits.guest.quota_bytes)} / {humanDuration(c.limits.guest.window_seconds)}, ≤ {humanSize(
									c.limits.guest.max_file_bytes
								)} each
							</dd>
						</dl>
					{/if}
					<p class="checked small muted">Checked <Time at={files?.checked_at} /> · every 15 s</p>
				{:else}
					<p class="small muted none">
						{files && !files.configured
							? 'No status: the files service isn’t reachable from the admin panel.'
							: 'No status from the files service right now.'}
					</p>
				{/if}
			</div>
			{#if filesStatus.length}
				<ConfigStatus statuses={filesStatus} {checking} onrecheck={recheck} />
			{/if}
		</aside>
	</div>
{/if}

<style>
	.lbl {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-quiet);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
	}
	.opt {
		text-transform: none;
		letter-spacing: 0;
		font-weight: 400;
		color: var(--fg-quiet);
		margin-left: var(--space-1);
	}
	.docs {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-4);
		align-items: baseline;
		font-size: var(--text-sm);
	}
	.docs a {
		font-family: var(--font-mono);
	}
	.notes {
		display: grid;
		gap: var(--space-3);
		max-width: 1080px;
	}
	.notes:has(*) {
		margin-bottom: var(--space-6);
	}
	.layout {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(260px, 320px);
		gap: var(--space-8);
		align-items: start;
		max-width: 1080px;
	}
	.form {
		display: grid;
		gap: var(--space-8);
		min-width: 0;
	}
	fieldset {
		border: 0;
		margin: 0;
		padding: 0;
		min-width: 0;
		display: grid;
		gap: var(--space-8);
	}
	.card {
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.switch {
		padding: var(--space-4);
		transition: border-color var(--motion-fast) var(--ease-standard);
	}
	.switch.on {
		border-color: color-mix(in oklab, var(--accent), transparent 50%);
	}
	.group {
		display: grid;
		gap: var(--space-3);
	}
	.group-head h2 {
		font-size: var(--text-base);
	}
	.group-head p {
		margin-top: var(--space-1);
		color: var(--fg-quiet);
		font-size: var(--text-sm);
		max-width: 64ch;
	}
	.fields {
		display: grid;
		gap: var(--space-5);
		padding: var(--space-4);
	}
	.row2 {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-4) var(--space-5);
		align-items: start;
	}
	.card.row2 {
		padding: var(--space-4);
	}
	.dim {
		opacity: 0.55;
	}
	.s3 {
		display: grid;
		gap: var(--space-5);
		padding-top: var(--space-4);
		border-top: 1px solid var(--border-soft);
	}
	.tgl {
		padding: var(--space-3) 0;
		border-top: 1px solid var(--border-soft);
		border-bottom: 1px solid var(--border-soft);
	}
	.means {
		font-family: var(--font-mono);
		color: var(--fg-2);
		margin-right: var(--space-1);
	}
	.field small code {
		font-size: var(--text-xs);
		overflow-wrap: anywhere;
	}
	input[aria-invalid='true'] {
		border-color: var(--danger);
	}
	.err {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		line-height: 1.4;
		color: var(--danger-text);
	}
	.keys {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		min-height: 30px;
		align-items: center;
	}
	.keys code {
		font-size: var(--text-sm);
	}
	.small {
		font-size: var(--text-xs);
	}
	.side {
		position: sticky;
		top: var(--space-6);
		display: grid;
		gap: var(--space-4);
	}
	.status {
		padding: var(--space-4);
		display: grid;
		gap: var(--space-3);
	}
	.st-head {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: var(--space-2);
	}
	.st-head h2 {
		font-size: var(--text-base);
	}
	.sub {
		font-size: var(--text-sm);
		padding-top: var(--space-3);
		border-top: 1px solid var(--border-soft);
	}
	dl {
		display: grid;
		grid-template-columns: max-content minmax(0, 1fr);
		gap: var(--space-2) var(--space-4);
		margin: 0;
		font-size: var(--text-sm);
	}
	dt {
		color: var(--fg-quiet);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		align-self: center;
	}
	dd {
		margin: 0;
		font-family: var(--font-mono);
		font-variant-numeric: tabular-nums;
		min-width: 0;
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-2);
		align-items: center;
	}
	.echo dd {
		font-size: var(--text-xs);
	}
	.wrap {
		overflow-wrap: anywhere;
	}
	[data-storage='ok'] .dot {
		background: var(--success);
	}
	[data-storage='failing'] .dot {
		background: var(--danger);
	}
	.meter {
		height: 6px;
		border-radius: 3px;
		background: var(--border-soft);
		overflow: hidden;
	}
	.meter span {
		display: block;
		height: 100%;
		background: var(--accent);
	}
	.meter.hot span {
		background: var(--danger);
	}
	.problems {
		margin: 0;
		padding-left: var(--space-4);
		color: var(--danger-text);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		line-height: 1.5;
		overflow-wrap: anywhere;
	}
	.checked {
		padding-top: var(--space-3);
		border-top: 1px solid var(--border-soft);
	}
	.bar {
		position: sticky;
		bottom: 0;
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3) var(--space-4);
		align-items: center;
		justify-content: space-between;
		padding: var(--space-3) var(--space-4);
		margin: 0 calc(-1 * var(--space-4));
		background: color-mix(in oklab, var(--bg), transparent 8%);
		backdrop-filter: blur(8px);
		border-top: 1px solid var(--border);
		z-index: 1;
	}
	.bar.dirty {
		border-top-color: var(--accent);
	}
	.meta {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.dirty .meta {
		color: var(--accent-text);
	}
	.btns {
		display: flex;
		gap: var(--space-2);
		margin-left: auto;
	}
	@media (max-width: 900px) {
		.layout {
			grid-template-columns: minmax(0, 1fr);
		}
		.side {
			position: static;
			order: -1;
		}
	}
	@media (max-width: 560px) {
		.row2 {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
