<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import {
		TELEMETRY_INFO_URL,
		TELEMETRY_WHY,
		endpointHost,
		reasonText,
		resultText,
		resultTone,
		type TelemetryStatus
	} from '$lib/telemetry';
	import Dialog from './Dialog.svelte';
	import Notice from './Notice.svelte';
	import Time from './Time.svelte';
	import Toggle from './Toggle.svelte';

	/** The Settings page's "Usage & health stats" card: the switch, the last
	 * send and the next report exactly as it would be sent. */
	let status = $state<TelemetryStatus | null>(null);
	let error = $state('');
	let on = $state(false);
	let saving = $state(false);
	let preview = $state('');
	let previewError = $state('');
	let loadingPreview = $state(false);
	let dialog: Dialog;

	let locked = $derived(!!status?.locked);
	let canSave = $derived(!!status?.config_file && !locked);
	let tone = $derived(resultTone(status?.last));

	function take(s: TelemetryStatus) {
		status = s;
		on = !!s.enabled;
	}

	async function load() {
		error = '';
		try {
			take(await api<TelemetryStatus>('GET', '/telemetry'));
		} catch (e) {
			error = errorText(e);
		}
	}
	onMount(load);

	async function toggle(v: boolean) {
		saving = true;
		try {
			take(await api<TelemetryStatus>('PUT', '/telemetry', { enabled: v }));
			toasts.push(v ? 'Usage & health stats are on.' : 'Usage & health stats are off. Nothing more is sent.');
		} catch (e) {
			on = !v;
			toasts.push(errorText(e), 'bad');
		} finally {
			saving = false;
		}
	}

	async function showPreview() {
		dialog.open();
		loadingPreview = true;
		previewError = '';
		try {
			preview = JSON.stringify(await api<unknown>('GET', '/telemetry/preview'), null, 2);
		} catch (e) {
			previewError = errorText(e);
		} finally {
			loadingPreview = false;
		}
	}
	let installID = $derived.by(() => {
		try {
			return (JSON.parse(preview) as { install_id?: string }).install_id ?? '';
		} catch {
			return '';
		}
	});
</script>

<section class="usage" aria-labelledby="usage-h" data-testid="usage-stats">
	<h2 id="usage-h">Usage &amp; health stats</h2>
	<p class="why">{TELEMETRY_WHY}</p>

	{#if error}
		<Notice tone="bad">
			{error}
			<button class="btn btn-small btn-ghost" type="button" onclick={load}>Try again</button>
		</Notice>
	{:else}
		<div class="card">
			<div class="row">
				<Toggle
					label="Send usage & health stats"
					detail="One small anonymous report a day, and one after each update. Counts are sent as ranges (11–50), never exact numbers."
					disabled={!status || !canSave || saving}
					bind:checked={on}
					onchange={toggle}
				/>
			</div>
			{#if locked}
				<div class="row">
					<Notice tone="info">
						<code>TELEMETRY=off</code> in the stack’s <code>.env</code> turns this off, whatever the switch
						says. Remove it there (then <code>docker compose up -d admin</code>) to use the switch.
					</Notice>
				</div>
			{:else if status && !status.config_file}
				<div class="row">
					<Notice tone="info">
						The switch is saved as <code>admin.telemetry</code> in the stack config file, which this panel
						isn’t set up to edit (<code>BOSON_CONFIG</code>). Set <code>TELEMETRY=off</code> in
						<code>.env</code> to turn it off instead.
					</Notice>
				</div>
			{/if}
			<dl class="row facts">
				<div>
					<dt>Last sent</dt>
					<dd>
						{#if status?.last}
							<Time at={status.last.at} />
							<span class="result {tone}" data-testid="usage-result">· {resultText(status.last)}</span>
						{:else}
							<span class="muted" data-testid="usage-result">{resultText(undefined)}</span>
						{/if}
					</dd>
				</div>
				<div>
					<dt>Next</dt>
					<dd>
						{#if status?.enabled && status.next_at}
							<Time at={status.next_at} />
							<span class="muted">· {reasonText(status.next_reason)}</span>
						{:else}
							<span class="muted">Not sending</span>
						{/if}
					</dd>
				</div>
			</dl>
			<div class="row actions">
				<button class="btn btn-small" type="button" onclick={showPreview}>See exactly what’s sent</button>
				<a class="info" href={TELEMETRY_INFO_URL} target="_blank" rel="noopener noreferrer"
					>What’s collected <span aria-hidden="true">↗</span></a
				>
			</div>
		</div>
	{/if}
</section>

<Dialog bind:this={dialog} title="What’s sent" wide>
	<div class="preview">
		<p class="hint">
			The next report, exactly as it would go to <code>{endpointHost(status?.endpoint)}</code>. It is built
			when it’s sent, so the numbers can change by then.
			{#if installID}
				To have this install’s reports deleted, email <a href="mailto:hi@boson.chat">hi@boson.chat</a> with
				its ID <code class="id">{installID}</code>.
			{/if}
		</p>
		{#if loadingPreview}
			<p class="muted">Putting the report together…</p>
		{:else if previewError}
			<Notice tone="bad">{previewError}</Notice>
		{:else}
			<!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable block needs focus to scroll by keyboard) -->
			<pre tabindex="0" aria-label="The next report (JSON)" data-testid="usage-preview">{preview}</pre>
		{/if}
		<div class="close">
			<button class="btn btn-small" type="button" onclick={() => dialog.close()}>Done</button>
		</div>
	</div>
</Dialog>

<style>
	.usage {
		display: grid;
		gap: var(--space-3);
		max-width: 760px;
		margin-top: var(--space-10);
	}
	h2 {
		font-size: var(--text-base);
	}
	.why {
		color: var(--fg-2);
		font-size: var(--text-sm);
		line-height: 1.6;
		max-width: 68ch;
	}
	.card {
		display: grid;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.row {
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}
	.row:first-child {
		padding: var(--space-4);
	}
	.row:last-child {
		border-bottom: 0;
	}
	.facts {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-3) var(--space-6);
		margin: 0;
	}
	dt {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		color: var(--fg-quiet);
	}
	dd {
		margin: 2px 0 0;
		font-size: var(--text-sm);
		color: var(--fg);
	}
	.result.ok {
		color: var(--success-text);
	}
	.result.bad {
		color: var(--danger-text);
	}
	.muted {
		color: var(--fg-quiet);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-4);
	}
	.info {
		font-size: var(--text-sm);
	}
	.preview {
		display: grid;
		gap: var(--space-3);
	}
	.hint {
		color: var(--fg-2);
		font-size: var(--text-sm);
		line-height: 1.55;
	}
	.id {
		word-break: break-all;
	}
	pre {
		margin: 0;
		max-height: min(60dvh, 520px);
		overflow: auto;
		padding: var(--space-3) var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--bg);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		line-height: 1.55;
		color: var(--fg);
		white-space: pre;
	}
	pre:focus-visible {
		outline: none;
		box-shadow: var(--focus-ring);
	}
	.close {
		display: flex;
		justify-content: flex-end;
	}
	@media (max-width: 640px) {
		.facts {
			grid-template-columns: minmax(0, 1fr);
		}
		/* no sideways scrolling on a phone: long values wrap */
		pre {
			white-space: pre-wrap;
			overflow-wrap: anywhere;
		}
	}
</style>
