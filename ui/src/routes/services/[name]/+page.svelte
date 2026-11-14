<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { page } from '$app/state';
	import { api } from '$lib/api';
	import { errorText } from '$lib/toasts.svelte';
	import { since as elapsed } from '$lib/format';
	import type { LogLine, ServiceInfo } from '$lib/types';
	import type { DropdownOption } from '$lib/dropdown';
	import Dropdown from '$lib/components/Dropdown.svelte';
	import ServiceActions from '$lib/components/ServiceActions.svelte';
	import State from '$lib/components/State.svelte';

	const MAX_LINES = 10000;
	let name = $derived(page.params.name ?? '');
	let svc = $state<ServiceInfo | null>(null);
	let svcError = $state('');
	let lines = $state<LogLine[]>([]);
	let lastAt = $state<string | null>(null);
	let logError = $state('');
	let loading = $state(false);
	let tail = $state('500');
	let follow = $state(true);
	let filter = $state('');
	let errorsOnly = $state(false);
	let wrap = $state(true);
	let atBottom = $state(true);
	let box = $state<HTMLElement>();
	let now = $state(Date.now());

	const tails: DropdownOption[] = [
		{ value: '100', label: 'Last 100 lines' },
		{ value: '500', label: 'Last 500 lines' },
		{ value: '1000', label: 'Last 1,000 lines' },
		{ value: '5000', label: 'Last 5,000 lines' }
	];

	async function loadSvc() {
		try {
			svc = (await api<{ service: ServiceInfo }>('GET', `/services/${encodeURIComponent(name)}`)).service;
			svcError = '';
		} catch (e) {
			svcError = errorText(e);
		}
	}

	async function loadLogs() {
		loading = true;
		try {
			const r = await api<{ lines: LogLine[]; last_at: string | null }>(
				'GET',
				`/services/${encodeURIComponent(name)}/logs?tail=${tail}`
			);
			lines = r.lines;
			lastAt = r.last_at;
			logError = '';
			await scrollEnd(true);
		} catch (e) {
			logError = errorText(e);
		} finally {
			loading = false;
		}
	}

	let polling = false;
	async function poll() {
		if (polling || !follow || !lastAt || document.visibilityState !== 'visible') return;
		polling = true;
		try {
			const r = await api<{ lines: LogLine[]; last_at: string | null }>(
				'GET',
				`/services/${encodeURIComponent(name)}/logs?tail=${tail}&since=${encodeURIComponent(lastAt)}`
			);
			const fresh = r.lines.filter((l) => !lastAt || !l.at || l.at > lastAt);
			if (fresh.length) {
				const all = lines.concat(fresh);
				lines = all.length > MAX_LINES ? all.slice(all.length - MAX_LINES) : all;
			}
			if (r.last_at) lastAt = r.last_at;
			logError = '';
			await scrollEnd(false);
		} catch (e) {
			logError = errorText(e);
		} finally {
			polling = false;
		}
	}

	async function scrollEnd(force: boolean) {
		await tick();
		if (box && (force || (follow && atBottom))) box.scrollTop = box.scrollHeight;
	}
	function onScroll() {
		if (!box) return;
		atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
	}

	$effect(() => {
		void name;
		svc = null;
		lines = [];
		lastAt = null;
		loadSvc();
		loadLogs();
	});
	onMount(() => {
		const t = setInterval(poll, 2000);
		const s = setInterval(() => {
			now = Date.now();
			if (document.visibilityState === 'visible') loadSvc();
		}, 15000);
		return () => {
			clearInterval(t);
			clearInterval(s);
		};
	});

	let needle = $derived(filter.trim().toLowerCase());
	let shown = $derived(
		lines.filter(
			(l) => (!errorsOnly || l.stream === 'stderr') && (!needle || l.text.toLowerCase().includes(needle))
		)
	);
	function clock(at: string) {
		if (!at) return '';
		const d = new Date(at);
		return (
			d.toLocaleTimeString('en-GB', { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0')
		);
	}
	let downloadHref = $derived(`/api/services/${encodeURIComponent(name)}/logs?download=1&tail=50000`);
</script>

<svelte:head><title>{svc?.service ?? name} logs · boson admin</title></svelte:head>

<p class="crumb"><a href="/services">Services</a></p>

{#if svcError && !svc}
	<State kind="error">
		{svcError}
		{#snippet action()}<a class="btn btn-small" href="/services">All services</a>{/snippet}
	</State>
{:else}
	<div class="page-head">
		<div>
			<h1>
				{svc?.service ?? name}
				{#if svc}
					<span
						class="tag {svc.state === 'running'
							? 'tag-ok'
							: svc.state === 'exited' && !svc.exit_code
								? ''
								: 'tag-bad'}">{svc.state}</span
					>
				{/if}
			</h1>
			{#if svc}
				<p class="meta mono">
					{[
						svc.name,
						svc.image,
						svc.state === 'running' && svc.started_at ? `up ${elapsed(svc.started_at, now)}` : '',
						svc.restart_count ? `${svc.restart_count} restarts` : ''
					]
						.filter(Boolean)
						.join(' · ')}
				</p>
			{/if}
		</div>
		{#if svc}<ServiceActions {svc} ondone={(s) => (s ? (svc = s) : loadSvc())} />{/if}
	</div>

	<div class="toolbar">
		<label class="search">
			<span class="sr-only">Filter lines</span>
			<input
				type="search"
				placeholder="Filter lines"
				bind:value={filter}
				spellcheck="false"
				autocomplete="off"
			/>
		</label>
		<Dropdown options={tails} bind:value={tail} label="How many lines" size="small" onchange={loadLogs} />
		<label class="chk"><input type="checkbox" bind:checked={errorsOnly} /> stderr only</label>
		<label class="chk"><input type="checkbox" bind:checked={wrap} /> Wrap</label>
		<label class="chk follow" class:on={follow}>
			<input
				type="checkbox"
				bind:checked={follow}
				onchange={() => {
					if (follow) scrollEnd(true);
				}}
			/>
			<span class="live" aria-hidden="true"></span> Follow
		</label>
		<a class="btn btn-small" href={downloadHref} download>Download</a>
	</div>

	{#if logError}<p class="lerr" role="alert">{logError}</p>{/if}

	<!-- svelte-ignore a11y_no_noninteractive_tabindex (focusable so the log scrolls with the keyboard) -->
	<div
		class="logs"
		class:wrap
		bind:this={box}
		onscroll={onScroll}
		tabindex="0"
		role="log"
		aria-label="{svc?.service ?? name} logs"
		aria-busy={loading}
	>
		{#if loading && !lines.length}
			<p class="note">Reading logs…</p>
		{:else if !lines.length}
			<p class="note">No output yet.</p>
		{:else if !shown.length}
			<p class="note">No lines match “{filter}”.</p>
		{:else}
			{#each shown as l, i (i)}
				<div class="ln" class:err={l.stream === 'stderr'}>
					<time datetime={l.at}>{clock(l.at)}</time><span class="tx">{l.text}</span>
				</div>
			{/each}
		{/if}
	</div>
	<p class="muted foot">
		{shown.length.toLocaleString()} of {lines.length.toLocaleString()} lines{follow
			? ', following (every 2 s)'
			: ''}. {#if follow && !atBottom}<button class="link" onclick={() => scrollEnd(true)}
				>Jump to newest</button
			>{/if}
	</p>
{/if}

<style>
	.crumb {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		margin-bottom: var(--space-3);
	}
	.crumb a {
		text-decoration: none;
	}
	.crumb a::before {
		content: '← ';
	}
	h1 {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		flex-wrap: wrap;
	}
	.meta {
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		overflow-wrap: anywhere;
	}
	.page-head {
		margin-bottom: var(--space-5);
	}
	.toolbar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3) var(--space-4);
		margin-bottom: var(--space-3);
	}
	.search {
		flex: 1 1 220px;
		max-width: 380px;
	}
	.search input {
		min-height: 32px;
		font-size: var(--text-sm);
	}
	.chk {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-2);
		cursor: pointer;
	}
	.chk input {
		accent-color: var(--accent);
	}
	.live {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--fg-3);
	}
	.follow.on .live {
		background: var(--success);
		animation: blink 1.6s steps(2, start) infinite;
	}
	.follow.on {
		color: var(--fg);
	}
	.logs {
		height: min(68vh, 760px);
		overflow: auto;
		padding: var(--space-3) 0;
		background: var(--bg);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		font-family: var(--font-mono);
		font-size: 12px;
		line-height: 1.6;
	}
	.ln {
		display: grid;
		grid-template-columns: 13ch minmax(0, 1fr);
		gap: var(--space-3);
		padding: 0 var(--space-4);
		white-space: pre;
	}
	.wrap .ln {
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.ln:hover {
		background: var(--surface-hover);
	}
	.ln time {
		color: var(--fg-3);
		user-select: none;
	}
	.tx {
		color: var(--fg-2);
	}
	.ln.err {
		box-shadow: inset 2px 0 0 var(--danger);
	}
	.ln.err .tx {
		color: var(--danger-text);
	}
	.note {
		padding: var(--space-4);
		color: var(--fg-quiet);
	}
	.lerr {
		color: var(--danger-text);
		font-size: var(--text-sm);
		margin-bottom: var(--space-2);
	}
	.foot {
		margin-top: var(--space-2);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}
	.link {
		background: none;
		border: 0;
		padding: 0;
		color: var(--accent-text);
		font: inherit;
		text-decoration: underline;
		cursor: pointer;
	}
	@keyframes blink {
		to {
			opacity: 0.3;
		}
	}
	@media (max-width: 640px) {
		.ln {
			grid-template-columns: minmax(0, 1fr);
			gap: 0;
			padding: 2px var(--space-3);
		}
		.logs {
			height: 70vh;
		}
	}
</style>
