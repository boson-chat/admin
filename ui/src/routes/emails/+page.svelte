<script lang="ts">
	import { onMount } from 'svelte';
	import { api, isUnsupported } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { stackConfig } from '$lib/stackconfig.svelte';
	import type { ConfigChange } from '$lib/types';
	import {
		EMAIL_TEMPLATES,
		MAX_BODY,
		MAX_SUBJECT,
		VAR_HELP,
		bodyKey,
		emailOk,
		insertAt,
		subjectKey,
		unknownVars,
		type EmailTemplateId
	} from '$lib/emails';
	import ColorPicker from '$lib/components/ColorPicker.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import State from '$lib/components/State.svelte';
	import Toggle from '$lib/components/Toggle.svelte';

	/** auth's email accent when auth.email.accent isn't set. */
	const DEFAULT_ACCENT = '#3b5bdb';

	let view = $derived(stackConfig.view);
	let loaded = $state(false);
	const key = (k: string) => view?.keys.find((x) => x.key === k);
	const val = (k: string) => {
		const x = key(k);
		return x?.set ? x.value : undefined;
	};
	let editable = $derived(!!key('auth.email.sender_name')?.editable);
	let branded = $derived(!!view?.keys.some((k) => k.key.startsWith('auth.email.') && k.set));
	let senderDefault = $derived.by(() => {
		const from = (val('auth.smtp.from') as string | undefined) ?? '';
		const m = /^\s*"?([^"<]+?)"?\s*</.exec(from);
		return m ? m[1] : 'the name in the SMTP from address';
	});

	// ---- settings ----
	let s = $state({ sender_name: '', accent: null as string | null, footer: '', logo: true });
	let sSaved = $state({ sender_name: '', accent: null as string | null, footer: '', logo: true });
	let sDirty = $derived(JSON.stringify(s) !== JSON.stringify(sSaved));
	let savingSettings = $state(false);

	// ---- templates ----
	type Draft = { subject: string; body: string };
	let selected = $state<EmailTemplateId>('verification');
	let drafts = $state<Record<string, Draft>>({});
	let saved = $state<Record<string, Draft>>({});
	let tpl = $derived(EMAIL_TEMPLATES.find((t) => t.id === selected)!);
	let draft = $derived(drafts[selected] ?? { subject: '', body: '' });
	let tDirty = (id: string) => JSON.stringify(drafts[id]) !== JSON.stringify(saved[id]);
	let custom = (id: string) => !!(saved[id]?.subject || saved[id]?.body);
	let savingTpl = $state(false);
	let unknown = $derived(unknownVars(draft.subject + '\n' + draft.body, tpl.vars));

	function fill() {
		const next = {
			sender_name: (val('auth.email.sender_name') as string) ?? '',
			accent: (val('auth.email.accent') as string) ?? null,
			footer: (val('auth.email.footer') as string) ?? '',
			logo: (val('auth.email.logo') as boolean | undefined) ?? true
		};
		s = { ...next };
		sSaved = { ...next };
		const d: Record<string, Draft> = {};
		for (const t of EMAIL_TEMPLATES)
			d[t.id] = {
				subject: (val(subjectKey(t.id)) as string) ?? '',
				body: (val(bodyKey(t.id)) as string) ?? ''
			};
		saved = structuredClone(d);
		drafts = structuredClone(d);
	}

	async function load() {
		await stackConfig.load();
		fill();
		loaded = true;
		refreshPreview();
	}
	onMount(load);

	async function saveChange(c: ConfigChange, msg: string) {
		try {
			await stackConfig.save(c);
			fill();
			toasts.push(msg);
			refreshPreview();
			return true;
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			if (e instanceof Error && /changed since you loaded/.test(e.message)) await stackConfig.load();
			return false;
		}
	}

	async function saveSettings() {
		savingSettings = true;
		const set: Record<string, string | boolean> = {};
		const unset: string[] = [];
		const put = (k: string, v: string | null, was: string | null) => {
			if (v === was) return;
			if (v) set['auth.email.' + k] = v;
			else unset.push('auth.email.' + k);
		};
		put('sender_name', s.sender_name.trim() || null, sSaved.sender_name || null);
		put('accent', s.accent, sSaved.accent);
		put('footer', s.footer.trim() || null, sSaved.footer || null);
		if (s.logo !== sSaved.logo || !branded) set['auth.email.logo'] = s.logo;
		await saveChange({ set, unset }, 'Email settings saved.');
		savingSettings = false;
	}

	async function saveTemplate() {
		savingTpl = true;
		const d = drafts[selected];
		const set: Record<string, string> = {};
		const unset: string[] = [];
		if (d.subject.trim()) set[subjectKey(selected)] = d.subject.trim();
		else if (saved[selected].subject) unset.push(subjectKey(selected));
		if (d.body.trim()) set[bodyKey(selected)] = d.body;
		else if (saved[selected].body) unset.push(bodyKey(selected));
		await saveChange({ set, unset }, `${tpl.label} email saved.`);
		savingTpl = false;
	}

	async function resetTemplate() {
		savingTpl = true;
		const unset = [subjectKey(selected), bodyKey(selected)].filter((k) => key(k)?.set);
		if (unset.length) await saveChange({ unset }, `${tpl.label} email is back to Boson’s wording.`);
		else drafts[selected] = { subject: '', body: '' };
		savingTpl = false;
	}

	// ---- variable chips ----
	let bodyEl = $state<HTMLTextAreaElement>();
	let subjectEl = $state<HTMLInputElement>();
	let lastField = $state<'subject' | 'body'>('body');
	function insertVar(v: string) {
		const el = lastField === 'subject' ? subjectEl : bodyEl;
		if (!el) return;
		const field = lastField;
		const r = insertAt(drafts[selected][field], el.selectionStart ?? 0, el.selectionEnd ?? 0, `{{${v}}}`);
		drafts[selected][field] = r.value;
		requestAnimationFrame(() => {
			el.focus();
			el.setSelectionRange(r.caret, r.caret);
		});
	}

	// ---- preview ----
	let preview = $state<{ subject: string; html: string; text: string } | null>(null);
	let previewError = $state('');
	let previewOff = $state('');
	let previewing = $state(false);
	let previewMode = $state<'html' | 'text'>('html');
	let timer: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;
	async function refreshPreview() {
		if (!session.info?.features.email_preview) {
			previewOff = 'off';
			return;
		}
		const id = ++seq;
		const d = drafts[selected] ?? { subject: '', body: '' };
		const body: Record<string, string> = { template: selected };
		if (d.subject.trim()) body.subject = d.subject.trim();
		if (d.body.trim()) body.body = d.body;
		previewing = true;
		try {
			const p = await api<{ subject: string; html: string; text: string }>('POST', '/emails/preview', body);
			if (id !== seq) return;
			preview = p;
			previewError = '';
			previewOff = '';
		} catch (e) {
			if (id !== seq) return;
			if (isUnsupported(e)) previewOff = e.message;
			else previewError = errorText(e);
		} finally {
			if (id === seq) previewing = false;
		}
	}
	$effect(() => {
		void selected;
		void draft.subject;
		void draft.body;
		if (!loaded) return;
		clearTimeout(timer);
		timer = setTimeout(refreshPreview, 450);
		return () => clearTimeout(timer);
	});

	// ---- send test ----
	let to = $state(session.info?.user.email ?? '');
	let sending = $state(false);
	async function sendTest(e: Event) {
		e.preventDefault();
		if (!emailOk(to)) {
			toasts.push('Enter one email address to send the test to.', 'bad');
			return;
		}
		sending = true;
		try {
			const r = await api<{ sent: boolean; message?: string }>('POST', '/emails/test', {
				template: selected,
				to: to.trim()
			});
			toasts.push(r.message || `Sent the ${tpl.label.toLowerCase()} email to ${to.trim()}.`);
		} catch (err) {
			toasts.push(errorText(err), 'bad');
		} finally {
			sending = false;
		}
	}
	let configOff = $derived(session.info?.features.config_file === false || view?.enabled === false);
</script>

<svelte:head><title>Emails · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Emails</h1>
		<p>
			What the auth service’s emails say and how they look: the sender, accent colour, footer and the wording
			of each message. The logo comes from <a href="/branding">Branding</a>.
		</p>
	</div>
</div>

{#if configOff}
	<State kind="off" title="Email settings live in the stack config file">
		Set <code>BOSON_CONFIG=/config/boson.yaml</code> on the admin service and mount the stack’s
		<code>config/</code> directory.
	</State>
{:else if !loaded}
	<State kind="loading" title="Loading email settings…" />
{:else}
	<section class="card settings" aria-labelledby="look-h">
		<div class="card-head">
			<h2 id="look-h">Sender and look</h2>
			{#if branded}
				<span class="tag tag-ok">Branded layout</span>
			{:else}
				<span class="tag">Plain layout</span>
			{/if}
		</div>
		{#if !branded}
			<p class="hint">
				Emails use auth’s plain layout until you save something here. Saving switches them to the branded
				layout with your logo, accent and footer.
			</p>
		{/if}
		<div class="grid">
			<label class="field">
				<span>Sender name</span>
				<input
					type="text"
					bind:value={s.sender_name}
					maxlength="64"
					placeholder={senderDefault}
					disabled={!editable}
				/>
				<small>Shown as who the email is from. Empty: {senderDefault}.</small>
			</label>
			<label class="field">
				<span>Footer</span>
				<input
					type="text"
					bind:value={s.footer}
					maxlength="200"
					placeholder="Example Chat · example.com"
					disabled={!editable}
				/>
				<small>One line under every email.</small>
			</label>
			<div class="field">
				<span id="accent-l">Accent</span>
				<div class="accent">
					<span class="chip" style:--c={s.accent ?? DEFAULT_ACCENT} aria-hidden="true"></span>
					<code>{s.accent ?? DEFAULT_ACCENT}</code>
					{#if !s.accent}<span class="muted small">default</span>{/if}
				</div>
				<ColorPicker bind:value={s.accent} labelledby="accent-l" disabled={!editable} />
				<small>Buttons and links. “No colour” uses auth’s default blue.</small>
			</div>
			<div class="field">
				<span>Logo</span>
				<Toggle
					label="Put the logo at the top"
					detail="The wordmark if there is one, else the square logo, from the Branding page."
					bind:checked={s.logo}
					disabled={!editable}
				/>
			</div>
		</div>
		<div class="row-end">
			<button class="btn btn-ghost" type="button" disabled={!sDirty || savingSettings} onclick={fill}
				>Discard</button
			>
			<button
				class="btn btn-primary"
				type="button"
				disabled={(!sDirty && branded) || savingSettings || !editable}
				onclick={saveSettings}>{savingSettings ? 'Saving…' : 'Save settings'}</button
			>
		</div>
	</section>

	<div class="layout">
		<section class="card" aria-labelledby="tpl-h">
			<h2 id="tpl-h" class="sr-only">Messages</h2>
			<div class="tabs" role="tablist" aria-label="Email">
				{#each EMAIL_TEMPLATES as t (t.id)}
					<button
						type="button"
						role="tab"
						id="tab-{t.id}"
						aria-selected={selected === t.id}
						aria-controls="tpl-panel"
						onclick={() => (selected = t.id)}
					>
						{t.label}
						{#if tDirty(t.id)}<span class="dot unsaved" title="Unsaved"></span>
						{:else if custom(t.id)}<span class="dot edited" title="Custom wording"></span>{/if}
					</button>
				{/each}
			</div>
			<div class="panel" id="tpl-panel" role="tabpanel" aria-labelledby="tab-{selected}">
				<p class="hint">{tpl.detail}</p>
				<label class="field">
					<span>Subject</span>
					<input
						bind:this={subjectEl}
						type="text"
						bind:value={drafts[selected].subject}
						onfocus={() => (lastField = 'subject')}
						maxlength={MAX_SUBJECT}
						placeholder="Boson’s default subject"
						disabled={!editable}
					/>
				</label>
				<div class="field">
					<span id="vars-l">Insert</span>
					<div class="chips" role="group" aria-labelledby="vars-l">
						{#each tpl.vars as v (v)}
							<button
								type="button"
								class="var"
								title={VAR_HELP[v]}
								disabled={!editable}
								onmousedown={(e) => e.preventDefault()}
								onclick={() => insertVar(v)}>{`{{${v}}}`}</button
							>
						{/each}
					</div>
				</div>
				<label class="field">
					<span>Message</span>
					<textarea
						bind:this={bodyEl}
						bind:value={drafts[selected].body}
						onfocus={() => (lastField = 'body')}
						rows="11"
						maxlength={MAX_BODY}
						placeholder="Boson’s default wording. Type to replace it. Blank lines start paragraphs; links become buttons."
						disabled={!editable}></textarea>
					<small
						>Plain text. A blank line starts a new paragraph and links are made clickable. {draft.body
							.length}/{MAX_BODY}</small
					>
				</label>
				{#if unknown.length}
					<Notice tone="warn">
						{unknown.map((u) => `{{${u}}}`).join(', ')}
						{unknown.length === 1 ? 'isn’t' : 'aren’t'} filled in for this email and will show as typed.
					</Notice>
				{/if}
				<div class="row-end">
					{#if custom(selected) || draft.subject || draft.body}
						<button
							type="button"
							class="btn btn-ghost"
							disabled={savingTpl || !editable}
							onclick={resetTemplate}>Reset to default</button
						>
					{/if}
					<button
						class="btn btn-primary"
						type="button"
						disabled={!tDirty(selected) || savingTpl || !editable}
						onclick={saveTemplate}>{savingTpl ? 'Saving…' : 'Save email'}</button
					>
				</div>
			</div>
		</section>

		<section class="card preview" aria-labelledby="pv-h">
			<div class="pv-head">
				<h2 id="pv-h">Preview</h2>
				<div class="seg" role="tablist" aria-label="Preview format">
					<button
						type="button"
						role="tab"
						aria-selected={previewMode === 'html'}
						onclick={() => (previewMode = 'html')}>Email</button
					>
					<button
						type="button"
						role="tab"
						aria-selected={previewMode === 'text'}
						onclick={() => (previewMode = 'text')}>Plain text</button
					>
				</div>
			</div>
			{#if previewOff}
				<Notice tone="info">
					{#if previewOff === 'off'}
						Previews and test emails go through the auth service’s internal listener. Set
						<code>AUTH_INTERNAL_URL</code> on the admin service (the stack sets it to
						<code>http://auth:8081</code>).
					{:else}
						{previewOff}.
					{/if}
				</Notice>
			{:else if previewError}
				<Notice tone="bad">{previewError}</Notice>
			{:else if preview}
				<p class="subject" aria-live="polite"><span class="muted">Subject</span> {preview.subject}</p>
				{#if previewMode === 'html'}
					<iframe
						class="frame"
						class:busy={previewing}
						title="Email preview"
						sandbox=""
						referrerpolicy="no-referrer"
						srcdoc={preview.html}
					></iframe>
				{:else}
					<pre class="text" class:busy={previewing}>{preview.text}</pre>
				{/if}
				<p class="hint">
					Example values fill in the variables. Unsaved edits show here too; colours and the footer show once
					saved.
				</p>
			{:else}
				<p class="muted small">Rendering…</p>
			{/if}
			<form class="test" onsubmit={sendTest}>
				<label class="field">
					<span>Send a test to</span>
					<input
						type="text"
						inputmode="email"
						autocomplete="email"
						bind:value={to}
						placeholder="you@example.com"
					/>
				</label>
				<button
					class="btn"
					disabled={sending || !!previewOff || !editable || !emailOk(to)}
					title={tDirty(selected) ? 'Sends the saved version' : undefined}
					>{sending ? 'Sending…' : 'Send test'}</button
				>
			</form>
			{#if tDirty(selected)}
				<p class="hint">The test uses the saved wording; save first to send your edits.</p>
			{/if}
		</section>
	</div>
{/if}

<style>
	.card {
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
		padding: var(--space-4) var(--space-5);
		display: grid;
		gap: var(--space-4);
		min-width: 0;
		align-content: start;
	}
	.card h2 {
		font-size: var(--text-base);
	}
	.card-head {
		display: flex;
		align-items: center;
		gap: var(--space-3);
	}
	.settings {
		max-width: 1040px;
		margin-bottom: var(--space-6);
	}
	.grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-5) var(--space-6);
	}
	.hint {
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.small {
		font-size: var(--text-xs);
	}
	.accent {
		display: flex;
		align-items: center;
		gap: var(--space-2);
	}
	.chip {
		width: 18px;
		height: 18px;
		border-radius: var(--radius-sm);
		background: var(--c);
		border: 1px solid var(--border-strong);
	}
	.row-end {
		display: flex;
		justify-content: flex-end;
		gap: var(--space-2);
	}
	.layout {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
		gap: var(--space-6);
		max-width: 1040px;
		align-items: start;
	}
	.tabs {
		display: flex;
		flex-wrap: wrap;
		gap: 2px;
		margin: calc(-1 * var(--space-4)) calc(-1 * var(--space-5)) 0;
		padding: 0 var(--space-3);
		border-bottom: 1px solid var(--border);
	}
	.tabs button {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		border: 0;
		background: none;
		padding: var(--space-3) var(--space-2);
		color: var(--fg-2);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		cursor: pointer;
		box-shadow: inset 0 -2px 0 transparent;
	}
	.tabs button[aria-selected='true'] {
		color: var(--fg);
		box-shadow: inset 0 -2px 0 var(--accent);
	}
	.tabs button:hover {
		color: var(--fg);
	}
	.dot.unsaved {
		background: var(--accent);
	}
	.dot.edited {
		background: var(--info);
	}
	.panel {
		display: grid;
		gap: var(--space-4);
	}
	textarea {
		width: 100%;
		min-height: 220px;
		resize: vertical;
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-sm);
		border: 1px solid var(--border);
		background: var(--surface);
		color: var(--fg);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		line-height: 1.55;
	}
	textarea:focus-visible {
		outline: none;
		border-color: var(--accent);
		box-shadow:
			0 0 0 1px var(--accent),
			0 0 0 4px var(--accent-dim);
	}
	textarea::placeholder {
		color: var(--fg-3);
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.var {
		border: 1px solid var(--border);
		background: var(--surface-raised);
		color: var(--accent-text);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		padding: 3px 8px;
		border-radius: 999px;
		cursor: pointer;
	}
	.var:hover:not(:disabled) {
		border-color: var(--accent);
	}
	.var:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	.pv-head {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}
	.seg {
		display: inline-flex;
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
		padding: 4px 10px;
		border-radius: var(--radius-sm);
		cursor: pointer;
	}
	.seg button[aria-selected='true'] {
		background: var(--surface-raised);
		color: var(--fg);
		box-shadow: inset 0 0 0 1px var(--border-strong);
	}
	.subject {
		font-weight: 600;
		font-size: var(--text-sm);
	}
	.subject .muted {
		font-family: var(--font-mono);
		font-weight: 400;
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: var(--tracking-label);
		margin-right: var(--space-2);
	}
	.frame {
		width: 100%;
		height: 460px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		/* emails are written for a white client */
		background: #fff;
		color-scheme: light;
	}
	.text {
		margin: 0;
		min-height: 200px;
		max-height: 460px;
		overflow: auto;
		padding: var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--bg);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		white-space: pre-wrap;
		word-break: break-word;
	}
	.busy {
		opacity: 0.6;
	}
	.test {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: var(--space-3);
		align-items: end;
		padding-top: var(--space-4);
		border-top: 1px solid var(--border-soft);
	}
	@media (max-width: 1000px) {
		.layout,
		.grid {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
