<script lang="ts">
	import { onMount } from 'svelte';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { stackConfig } from '$lib/stackconfig.svelte';
	import type { ConfigServiceStatus, ConfigView } from '$lib/types';
	import type { DropdownOption } from '$lib/dropdown';
	import {
		IMAGE_SLOTS,
		MAX_BLUR,
		MAX_RADIUS,
		PANES,
		PANE_LABELS,
		POSITIONS,
		THEME_TOKENS,
		TOKEN_GROUPS,
		builtin,
		clean,
		contrastIssues,
		emptyTheme,
		paneSlot,
		sameTheme,
		type ImageSlot,
		type NetworkTheme,
		type PaneName,
		type PanePosition,
		type ThemeFont,
		type ThemeToken
	} from '$lib/theme';
	import ConfigStatus from '$lib/components/ConfigStatus.svelte';
	import Confirm from '$lib/components/Confirm.svelte';
	import Dropdown from '$lib/components/Dropdown.svelte';
	import ImageSlotField from '$lib/components/ImageSlot.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import State from '$lib/components/State.svelte';
	import ThemePreview from '$lib/components/ThemePreview.svelte';
	import TokenRow from '$lib/components/TokenRow.svelte';

	const THEME_KEY = 'branding.theme';
	const IMAGES_KEY = 'branding.images';

	let view = $derived(stackConfig.view);
	let loaded = $state(false);
	let saving = $state(false);
	let resetting = $state(false);
	let checking = $state(false);
	let previewView = $state<'client' | 'login'>('client');

	let editable = $derived(!!view?.keys.find((k) => k.key === THEME_KEY)?.editable);
	let savedTheme = $derived.by((): NetworkTheme => {
		const k = view?.keys.find((x) => x.key === THEME_KEY);
		return k?.set && k.value ? (k.value as unknown as NetworkTheme) : emptyTheme();
	});
	let themeSet = $derived(!!view?.keys.find((k) => k.key === THEME_KEY)?.set);
	let draft = $state<NetworkTheme>(emptyTheme());
	let dirty = $derived(!sameTheme(draft, savedTheme));

	let images = $derived.by(() => {
		const k = view?.keys.find((x) => x.key === IMAGES_KEY);
		const files = (k?.value ?? {}) as unknown as Record<string, string>;
		const out: Partial<Record<ImageSlot, string>> = {};
		for (const s of IMAGE_SLOTS)
			if (files[s]) out[s] = '/api/branding/assets/' + encodeURIComponent(files[s]);
		return out;
	});
	let network = $derived(
		(view?.keys.find((k) => k.key === 'network.name')?.value as string | undefined) || 'Your network'
	);
	let base = $derived(builtin(draft.base));
	let issues = $derived(contrastIssues(draft));
	let authStatus = $derived<ConfigServiceStatus[]>(
		(stackConfig.statuses ?? []).filter((s) => s.name === 'auth')
	);

	function fill() {
		draft = structuredClone($state.snapshot(savedTheme));
	}
	async function load() {
		await stackConfig.load();
		fill();
		loaded = true;
	}
	onMount(load);

	// ---- editing helpers ----
	function color(token: ThemeToken) {
		return draft.colors?.[token] ?? null;
	}
	function setColor(token: ThemeToken, v: string | null) {
		const colors = { ...(draft.colors ?? {}) };
		if (v) colors[token] = v;
		else delete colors[token];
		draft.colors = colors;
	}
	function pane(p: PaneName) {
		return draft.panes?.[p] ?? {};
	}
	function setPane(p: PaneName, patch: Record<string, unknown>) {
		const next = { ...pane(p), ...patch };
		for (const [k, v] of Object.entries(next))
			if (v === undefined || v === null) delete (next as Record<string, unknown>)[k];
		draft.panes = { ...(draft.panes ?? {}), [p]: next };
	}

	const fonts: DropdownOption<ThemeFont>[] = [
		{ value: 'boson', label: 'Boson', description: 'Inter for text, JetBrains Mono for headings' },
		{ value: 'system', label: 'System', description: 'The device’s own UI font' }
	];
	const positions: DropdownOption<PanePosition>[] = POSITIONS.map((p) => ({
		value: p,
		label: p[0].toUpperCase() + p.slice(1)
	}));

	// ---- saving ----
	async function follow() {
		for (const wait of [800, 2000, 4000]) {
			await new Promise((r) => setTimeout(r, wait));
			await recheck();
			if (!stackConfig.statuses?.some((s) => s.name === 'auth' && s.state === 'pending')) return;
		}
	}
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
	async function save() {
		saving = true;
		try {
			await stackConfig.save({ set: { [THEME_KEY]: clean($state.snapshot(draft) as NetworkTheme) } });
			fill();
			toasts.push('Theme saved. The auth service serves it to Boson apps within seconds.');
			follow();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			if (e instanceof Error && /changed since you loaded/.test(e.message)) await stackConfig.load();
		} finally {
			saving = false;
		}
	}
	async function resetTheme() {
		resetting = true;
		try {
			await stackConfig.save({ unset: [THEME_KEY] });
			fill();
			toasts.push('The theme is back to Boson’s default. Images stay until you remove them.');
			follow();
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			throw e;
		} finally {
			resetting = false;
		}
	}
	function imageSaved(v: ConfigView) {
		stackConfig.view = v;
		follow();
	}
	let configOff = $derived(session.info?.features.config_file === false || view?.enabled === false);
</script>

<svelte:head><title>Branding · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Branding</h1>
		<p>
			How Boson apps look on this network: colours, logo and backgrounds. People see it while they’re on this
			server, on its sign-in screen and on its directory listing. They can switch network themes off.
		</p>
	</div>
</div>

{#if configOff}
	<State kind="off" title="Branding lives in the stack config file">
		Set <code>BOSON_CONFIG=/config/boson.yaml</code> on the admin service and mount the stack’s
		<code>config/</code> directory read-write. Images are written to <code>config/branding/</code>.
	</State>
{:else if !loaded}
	<State kind="loading" title="Loading the theme…" />
{:else if stackConfig.error && !view}
	<State kind="error">
		{stackConfig.error}
		{#snippet action()}<button class="btn btn-small" onclick={load}>Try again</button>{/snippet}
	</State>
{:else}
	{#if view?.error}
		<div class="note">
			<Notice tone="bad">
				The config file has an error (line {view.error.line ?? '?'}): {view.error.message}. Fix it on the
				<a href="/config">Stack config</a> page before saving.
			</Notice>
		</div>
	{/if}
	<div class="layout">
		<div class="editor">
			<section class="card" aria-labelledby="look-h">
				<h2 id="look-h">Look</h2>
				<div class="field-row">
					<span class="lbl" id="base-l">Base</span>
					<div class="seg" role="radiogroup" aria-labelledby="base-l">
						{#each ['dark', 'light'] as const as b (b)}
							<button
								type="button"
								role="radio"
								aria-checked={draft.base === b}
								disabled={!editable}
								onclick={() => (draft.base = b)}>{b === 'dark' ? 'Dark' : 'Light'}</button
							>
						{/each}
					</div>
					<p class="hint">Colours you don’t set come from Boson’s {base.name} theme.</p>
				</div>
				<div class="field-row">
					<span class="lbl" id="font-l">Font</span>
					<Dropdown
						options={fonts}
						value={draft.font ?? 'boson'}
						labelledby="font-l"
						disabled={!editable}
						onchange={(v) => (draft.font = v)}
					/>
				</div>
				<div class="field-row">
					<label class="lbl" for="radius">Corners</label>
					<div class="range">
						<input
							id="radius"
							type="range"
							min="0"
							max={MAX_RADIUS}
							step="1"
							value={draft.radius ?? base.radius}
							disabled={!editable}
							oninput={(e) => (draft.radius = +e.currentTarget.value)}
						/>
						<output for="radius">{draft.radius ?? base.radius} px</output>
					</div>
				</div>
				<div class="field-row">
					<span class="lbl">Nick colours</span>
					<div class="pair2">
						<label class="range">
							<span class="sub">Saturation</span>
							<input
								type="range"
								min="0"
								max="100"
								value={draft.nick?.saturation ?? base.nick.saturation}
								disabled={!editable}
								oninput={(e) =>
									(draft.nick = {
										saturation: +e.currentTarget.value,
										lightness: draft.nick?.lightness ?? base.nick.lightness
									})}
							/>
							<output>{draft.nick?.saturation ?? base.nick.saturation}%</output>
						</label>
						<label class="range">
							<span class="sub">Lightness</span>
							<input
								type="range"
								min="0"
								max="100"
								value={draft.nick?.lightness ?? base.nick.lightness}
								disabled={!editable}
								oninput={(e) =>
									(draft.nick = {
										saturation: draft.nick?.saturation ?? base.nick.saturation,
										lightness: +e.currentTarget.value
									})}
							/>
							<output>{draft.nick?.lightness ?? base.nick.lightness}%</output>
						</label>
					</div>
					<p class="hint">The hue comes from each nick. Light themes need darker nicks.</p>
				</div>
			</section>

			<section class="card" aria-labelledby="colors-h">
				<div class="card-head">
					<h2 id="colors-h">Colours</h2>
					<p class="hint">Set only the ones you want to change.</p>
				</div>
				{#each TOKEN_GROUPS as g (g)}
					<h3 class="group">{g}</h3>
					<div class="tokens">
						{#each THEME_TOKENS.filter((t) => t.group === g) as t (t.key)}
							<TokenRow
								label={t.label}
								token={t.key}
								fallback={base.colors[t.key]}
								disabled={!editable}
								bind:value={() => color(t.key), (v) => setColor(t.key, v)}
							/>
						{/each}
					</div>
				{/each}
			</section>

			<section class="card" aria-labelledby="img-h">
				<div class="card-head">
					<h2 id="img-h">Logo and sign-in</h2>
					<p class="hint">
						PNG, JPEG, WebP, GIF or SVG, up to 2 MB and 4096 px. Saved as soon as you pick one.
					</p>
				</div>
				<div class="slots">
					<ImageSlotField
						slot="logo"
						label="Logo"
						hint="A square mark: the server’s rail tile, the sidebar and the sign-in screen."
						url={images.logo}
						disabled={!editable}
						onsaved={imageSaved}
					/>
					<ImageSlotField
						slot="logo_wide"
						label="Wordmark"
						hint="Optional wide logo for headers and emails."
						url={images.logo_wide}
						wide
						disabled={!editable}
						onsaved={imageSaved}
					/>
					<ImageSlotField
						slot="login_background"
						label="Sign-in background"
						hint="Behind “Sign in to {network}” and the sign-up form."
						url={images.login_background}
						wide
						cover
						disabled={!editable}
						onsaved={imageSaved}
					/>
				</div>
			</section>

			<section class="card" aria-labelledby="panes-h">
				<div class="card-head">
					<h2 id="panes-h">Pane backgrounds</h2>
					<p class="hint">
						A colour or an image behind each part of the app. Dim keeps text readable over an image.
					</p>
				</div>
				{#each PANES as p (p)}
					{@const pn = pane(p)}
					{@const img = images[paneSlot(p)]}
					<details class="pane" open={p === 'messages'}>
						<summary>
							<span>{PANE_LABELS[p]}</span>
							<span class="tags">
								{#if pn.bg}<span class="tag">colour</span>{/if}
								{#if img}<span class="tag tag-accent">image</span>{/if}
							</span>
						</summary>
						<div class="pane-body">
							<TokenRow
								label="Colour"
								token="pane-{p}-bg"
								fallback={p === 'channels' || p === 'members' ? base.colors.surface : base.colors.bg}
								disabled={!editable}
								bind:value={() => pn.bg ?? null, (v) => setPane(p, { bg: v ?? undefined })}
							/>
							<ImageSlotField
								slot={paneSlot(p)}
								label="Image"
								url={img}
								wide
								cover
								disabled={!editable}
								onsaved={imageSaved}
							/>
							<div class="pane-ctl" class:off={!img}>
								<label class="range">
									<span class="sub">Dim</span>
									<input
										type="range"
										min="0"
										max="1"
										step="0.05"
										value={pn.dim ?? 0.6}
										disabled={!editable || !img}
										oninput={(e) => setPane(p, { dim: +e.currentTarget.value })}
									/>
									<output>{Math.round((pn.dim ?? 0.6) * 100)}%</output>
								</label>
								<label class="range">
									<span class="sub">Blur</span>
									<input
										type="range"
										min="0"
										max={MAX_BLUR}
										step="1"
										value={pn.blur ?? 0}
										disabled={!editable || !img}
										oninput={(e) => setPane(p, { blur: +e.currentTarget.value })}
									/>
									<output>{pn.blur ?? 0} px</output>
								</label>
								<div class="range">
									<span class="sub" id="pos-{p}">Anchor</span>
									<Dropdown
										options={positions}
										value={pn.position ?? 'center'}
										labelledby="pos-{p}"
										size="small"
										disabled={!editable || !img}
										onchange={(v) => setPane(p, { position: v })}
									/>
								</div>
							</div>
						</div>
					</details>
				{/each}
			</section>
		</div>

		<aside class="preview-col" aria-label="Preview">
			<div class="preview-head">
				<div class="seg" role="tablist" aria-label="Preview">
					<button
						type="button"
						role="tab"
						aria-selected={previewView === 'client'}
						onclick={() => (previewView = 'client')}>App</button
					>
					<button
						type="button"
						role="tab"
						aria-selected={previewView === 'login'}
						onclick={() => (previewView = 'login')}>Sign-in screen</button
					>
				</div>
				<span class="live">{dirty ? 'Unsaved preview' : 'As saved'}</span>
			</div>
			<ThemePreview theme={draft} {images} {network} view={previewView} />
			{#if issues.length}
				<div class="contrast">
					<Notice tone="warn">
						<strong>Hard to read:</strong>
						<ul>
							{#each issues as i (i.label)}
								<li>
									{i.label} is <b>{i.ratio.toFixed(2)}:1</b>; text needs 4.5:1.
									{#if i.guard}Boson apps will ignore this theme’s text colours and keep each person’s own.{/if}
								</li>
							{/each}
						</ul>
					</Notice>
				</div>
			{/if}
			{#if authStatus.length}
				<div class="status">
					<ConfigStatus statuses={authStatus} {checking} onrecheck={recheck} />
				</div>
			{/if}
		</aside>
	</div>

	<div class="bar" class:dirty>
		<p class="meta" aria-live="polite">
			{#if !editable}
				You can look but not change it: saving needs <code>settings.manage</code>.
			{:else if dirty}
				Unsaved theme changes
			{:else if themeSet}
				Saved in <code>branding.theme</code> of the stack config file.
			{:else}
				Using Boson’s default look.
			{/if}
		</p>
		<div class="btns">
			{#if themeSet && editable}
				<Confirm title="Reset the theme?" action="Reset" danger onconfirm={resetTheme}>
					{#snippet trigger(open)}
						<button class="btn btn-ghost" type="button" disabled={resetting || saving} onclick={open}
							>Reset to default</button
						>
					{/snippet}
					Colours, corners, font and pane settings go back to Boson’s default. Uploaded images stay until you remove
					them.
				</Confirm>
			{/if}
			<button class="btn btn-ghost" type="button" disabled={!dirty || saving} onclick={fill}>Discard</button>
			<button class="btn btn-primary" type="button" disabled={!dirty || saving || !editable} onclick={save}
				>{saving ? 'Saving…' : 'Save theme'}</button
			>
		</div>
	</div>
{/if}

<style>
	.note {
		margin-bottom: var(--space-6);
		max-width: 760px;
	}
	.layout {
		display: grid;
		grid-template-columns: minmax(320px, 380px) minmax(0, 1fr);
		gap: var(--space-8);
		align-items: start;
	}
	.editor {
		display: grid;
		gap: var(--space-6);
		min-width: 0;
	}
	.card {
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
		padding: var(--space-4) var(--space-5);
		display: grid;
		gap: var(--space-3);
	}
	.card h2 {
		font-size: var(--text-base);
	}
	.card-head {
		display: grid;
		gap: var(--space-1);
	}
	.group {
		margin-top: var(--space-2);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 500;
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		color: var(--fg-quiet);
	}
	.hint {
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.field-row {
		display: grid;
		grid-template-columns: 96px minmax(0, 1fr);
		gap: var(--space-1) var(--space-3);
		align-items: center;
	}
	.field-row .hint {
		grid-column: 2;
	}
	.lbl {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
	}
	.seg {
		display: inline-flex;
		justify-self: start;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		padding: 2px;
		gap: 2px;
		background: var(--bg);
	}
	.seg button {
		border: 0;
		background: transparent;
		color: var(--fg-2);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		padding: 5px 12px;
		border-radius: var(--radius-sm);
		cursor: pointer;
	}
	.seg button[aria-checked='true'],
	.seg button[aria-selected='true'] {
		background: var(--surface-raised);
		color: var(--fg);
		box-shadow: inset 0 0 0 1px var(--border-strong);
	}
	.seg button:disabled {
		cursor: not-allowed;
		opacity: 0.6;
	}
	.range {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 48px;
		align-items: center;
		gap: var(--space-2);
	}
	.range .sub {
		grid-column: 1 / -1;
		font-size: var(--text-xs);
		color: var(--fg-2);
	}
	.range output {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-2);
		text-align: right;
	}
	input[type='range'] {
		width: 100%;
		accent-color: var(--accent);
	}
	.pair2 {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: var(--space-3);
	}
	.slots {
		display: grid;
		gap: var(--space-4);
	}
	details.pane {
		border-top: 1px solid var(--border-soft);
	}
	details.pane:first-of-type {
		border-top: 0;
	}
	summary {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: var(--space-2) 0;
		cursor: pointer;
		font-weight: 600;
		font-size: var(--text-sm);
		list-style: none;
	}
	summary::-webkit-details-marker {
		display: none;
	}
	summary::before {
		content: '▸';
		color: var(--fg-3);
		margin-right: var(--space-2);
		transition: transform var(--motion-fast) var(--ease-standard);
	}
	details[open] summary::before {
		transform: rotate(90deg);
	}
	summary > span:first-of-type {
		margin-right: auto;
	}
	.tags {
		display: flex;
		gap: var(--space-1);
	}
	.pane-body {
		display: grid;
		gap: var(--space-3);
		padding: 0 0 var(--space-4);
	}
	.pane-ctl {
		display: grid;
		grid-template-columns: 1fr 1fr 1fr;
		gap: var(--space-3);
		align-items: end;
	}
	.pane-ctl.off {
		opacity: 0.55;
	}
	.pane-ctl .range:last-child {
		grid-template-columns: minmax(0, 1fr);
	}

	.preview-col {
		position: sticky;
		top: var(--space-6);
		display: grid;
		gap: var(--space-4);
		min-width: 0;
	}
	.preview-head {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: var(--space-3);
	}
	.live {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.contrast ul {
		margin: var(--space-1) 0 0;
		padding-left: var(--space-4);
	}

	.bar {
		position: sticky;
		bottom: 0;
		z-index: 5;
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3) var(--space-4);
		align-items: center;
		justify-content: space-between;
		padding: var(--space-3) var(--space-4);
		margin: var(--space-8) calc(-1 * var(--space-4)) 0;
		background: color-mix(in oklab, var(--bg), transparent 8%);
		backdrop-filter: blur(8px);
		border-top: 1px solid var(--border);
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
	}

	@media (max-width: 1100px) {
		.layout {
			grid-template-columns: minmax(0, 1fr);
		}
		.preview-col {
			position: static;
			order: -1;
		}
	}
</style>
