<script lang="ts">
	import { onDestroy, onMount, untrack } from 'svelte';
	import { beforeNavigate } from '$app/navigation';
	import { api, ApiError, isUnsupported } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { stackConfig } from '$lib/stackconfig.svelte';
	import type { ConfigChange, ConfigView } from '$lib/types';
	import MjmlEditor from '$lib/components/MjmlEditor.svelte';
	import {
		LAYOUT,
		PLACEHOLDER_HELP,
		placeholdersFor,
		type CompileResult,
		type Diagnostic
	} from '$lib/mjml/compile';
	import { browserWorker, mjmlCompiler, type MjmlCompiler } from '$lib/mjml/compiler';
	import {
		afterRemove,
		afterSave,
		discard as discardDraft,
		initialMode,
		isDirty,
		loaded as loadedDraft,
		previewHTML,
		resetTo,
		type Drafts,
		type Mode
	} from '$lib/mjml/drafts';
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
	import {
		brandingPreviews,
		frameDoc,
		livePreview,
		previewField,
		previewRequest,
		type EmailPreview,
		type PreviewField,
		type PreviewRequest
	} from '$lib/livepreview';
	import Dropdown from '$lib/components/Dropdown.svelte';
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
	/** The tab: the MJML layout or one email. */
	let selected = $state<EmailTemplateId | typeof LAYOUT>('verification');
	/** The email the preview shows: the selected one, or the last one while on the layout. */
	let previewTpl = $state<EmailTemplateId>('verification');
	$effect(() => {
		if (selected !== LAYOUT) previewTpl = selected;
	});
	let tplId = $derived(selected === LAYOUT ? previewTpl : selected);
	let drafts = $state<Record<string, Draft>>({});
	let saved = $state<Record<string, Draft>>({});
	let tpl = $derived(EMAIL_TEMPLATES.find((t) => t.id === tplId)!);
	let draft = $derived(drafts[tplId] ?? { subject: '', body: '' });
	let tDirty = (id: string) => JSON.stringify(drafts[id]) !== JSON.stringify(saved[id]);
	let custom = (id: string) => !!(saved[id]?.subject || saved[id]?.body);
	let savingTpl = $state(false);
	let unknown = $derived(unknownVars(draft.subject + '\n' + draft.body, tpl.vars));

	// ---- MJML (layout + per email) ----
	const htmlKey = (part: string) =>
		part === LAYOUT ? 'auth.email.layout.html' : `auth.email.templates.${part}.html`;
	/** The part has its own MJML saved. */
	const hasOverride = (part: string) => !!key(htmlKey(part))?.set;
	let modes = $state<Record<string, Mode>>({});
	const modeOf = (id: string): Mode => modes[id] ?? initialMode(hasOverride(id));
	let mj = $state<Drafts>({});
	/** Why a part's MJML couldn't be loaded. */
	let mjLoadError = $state<Record<string, string>>({});
	let savingMj = $state(false);
	let mjError = $state('');
	/** The MJML on screen: the layout, or an email in MJML mode. */
	let activePart = $derived(selected === LAYOUT ? LAYOUT : modeOf(selected) === 'mjml' ? selected : null);
	let mjDirty = $derived(Object.values(mj).some(isDirty));
	let dirty = $derived(sDirty || mjDirty || EMAIL_TEMPLATES.some((t) => tDirty(t.id)));
	const partDirty = (id: string) => tDirty(id) || isDirty(mj[id]);

	const defaults: Record<string, string> = {};
	async function defaultSource(part: string): Promise<string> {
		if (defaults[part] === undefined) {
			const r = await api<{ mjml: string }>('GET', `/emails/default-mjml?part=${encodeURIComponent(part)}`);
			defaults[part] = r.mjml;
		}
		return defaults[part];
	}

	/** Loads a part's saved MJML, or Boson's default when it has none. */
	async function ensurePart(part: string) {
		if (mj[part] || loadingParts.has(part)) return;
		loadingParts.add(part);
		delete mjLoadError[part];
		try {
			let src: string | null = null;
			if (hasOverride(part)) {
				const r = await api<{ set: boolean; mjml?: string }>('GET', `/emails/mjml/${part}`);
				src = r.set ? (r.mjml ?? '') : null;
			}
			let fallback = '';
			if (src === null) fallback = await defaultSource(part);
			mj[part] = loadedDraft(src, fallback);
		} catch (e) {
			mjLoadError[part] = isUnsupported(e)
				? `${e.message}. Boson’s default MJML comes from auth; update auth to edit it here.`
				: errorText(e);
		} finally {
			loadingParts.delete(part);
		}
	}
	const loadingParts = new Set<string>();
	$effect(() => {
		const p = activePart;
		if (p && loaded) untrack(() => void ensurePart(p));
	});

	// Compile in the worker as you type (debounced); keep the last good
	// HTML for the preview while the source has errors.
	type Compiled = { source: string; html: string; diagnostics: Diagnostic[]; ok: boolean };
	let compiled = $state<Record<string, Compiled | undefined>>({});
	let compiling = $state(false);
	let compileError = $state('');
	let compiler: MjmlCompiler | undefined;
	const getCompiler = () => (compiler ??= mjmlCompiler(browserWorker));
	const lc = livePreview<{ part: string; source: string }, CompileResult>({
		fetch: (i, signal) => getCompiler().compile(i.source, i.part, signal),
		onResult(r, i) {
			compiled[i.part] = {
				source: i.source,
				html: r.html || compiled[i.part]?.html || '',
				diagnostics: r.diagnostics,
				ok: r.ok
			};
			compileError = '';
		},
		onError: (e) => (compileError = errorText(e)),
		onLoading: (b) => (compiling = b),
		delay: 250
	});
	onDestroy(() => {
		lc.cancel();
		compiler?.dispose();
	});
	let editorSource = $derived(activePart ? mj[activePart]?.draft : undefined);
	let lastCompiled = '';
	$effect(() => {
		const p = activePart;
		const src = editorSource;
		if (!p || src === undefined) return;
		untrack(() => {
			if (compiled[p]?.source === src) return;
			// A part just opened compiles at once; typing waits for a pause.
			if (p !== lastCompiled || !compiled[p]) {
				lastCompiled = p;
				void lc.now({ part: p, source: src });
			} else lc.schedule({ part: p, source: src });
		});
	});
	let current = $derived(activePart ? compiled[activePart] : undefined);
	let diagnostics = $derived(current?.diagnostics ?? []);
	let errorCount = $derived(diagnostics.filter((d) => d.severity === 'error').length);
	let warnCount = $derived(diagnostics.length - errorCount);
	let upToDate = $derived(!!current && current.source === editorSource && !compiling);
	let editor = $state<MjmlEditor>();

	const partLabel = (part: string) =>
		part === LAYOUT ? 'Layout' : (EMAIL_TEMPLATES.find((t) => t.id === part)?.label ?? part);

	async function saveMjml() {
		const part = activePart;
		const d = part ? mj[part] : undefined;
		if (!part || !d) return;
		savingMj = true;
		mjError = '';
		try {
			// The subject (and message) of an email in MJML mode save too.
			if (part !== LAYOUT && tDirty(part) && !(await saveChange(templateChange(part), '', part))) return;
			const c = await getCompiler().compile(d.draft, part);
			compiled[part] = {
				source: d.draft,
				html: c.html || compiled[part]?.html || '',
				diagnostics: c.diagnostics,
				ok: c.ok
			};
			if (!c.ok) {
				toasts.push('Fix the MJML errors before saving.', 'bad');
				return;
			}
			const etag = stackConfig.view?.etag;
			const r = await api<{ config: ConfigView }>(
				'PUT',
				`/emails/mjml/${part}`,
				{ mjml: d.draft, html: c.html },
				etag ? { 'If-Match': etag } : undefined
			);
			stackConfig.view = r.config;
			mj[part] = afterSave(mj[part]!, d.draft);
			modes[part] = 'mjml';
			toasts.push(part === LAYOUT ? 'Layout saved.' : `${partLabel(part)} email saved with its own MJML.`);
		} catch (e) {
			if (e instanceof ApiError && e.status === 400 && (e.field === 'mjml' || e.field === 'html'))
				mjError = e.message;
			toasts.push(errorText(e), 'bad');
			if (e instanceof ApiError && e.status === 412) await stackConfig.load();
		} finally {
			savingMj = false;
		}
	}

	async function removeMjml() {
		const part = activePart;
		if (!part) return;
		const what =
			part === LAYOUT
				? 'Use Boson’s built-in layout again? Your layout MJML is deleted.'
				: `Remove the ${partLabel(part).toLowerCase()} email’s own MJML? It goes back to the layout and your text.`;
		if (!confirm(what)) return;
		savingMj = true;
		mjError = '';
		try {
			const etag = stackConfig.view?.etag;
			const r = await api<{ config: ConfigView }>(
				'DELETE',
				`/emails/mjml/${part}`,
				undefined,
				etag ? { 'If-Match': etag } : undefined
			);
			stackConfig.view = r.config;
			mj[part] = afterRemove(await defaultSource(part).catch(() => ''));
			if (part !== LAYOUT) modes[part] = 'text';
			toasts.push(
				part === LAYOUT
					? 'Emails use Boson’s built-in layout again.'
					: `${partLabel(part)} email uses the layout again.`
			);
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			if (e instanceof ApiError && e.status === 412) await stackConfig.load();
		} finally {
			savingMj = false;
		}
	}

	async function resetMjml() {
		const part = activePart;
		if (!part || !mj[part]) return;
		try {
			mj[part] = resetTo(mj[part]!, await defaultSource(part));
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
	}
	const discardMjml = () => {
		if (activePart && mj[activePart]) mj[activePart] = discardDraft(mj[activePart]!);
	};
	function setDraft(part: string, v: string) {
		const d = mj[part];
		if (d && d.draft !== v) mj[part] = { ...d, draft: v };
	}
	const sizeKB = (s: string) => (new Blob([s]).size / 1024).toFixed(1);

	/**
	 * Reads the saved config into the form. keep: unsaved edits survive the
	 * reload except in what was just saved ('settings' or a template id).
	 */
	function fill(reset: 'all' | 'settings' | string = 'all') {
		const next = {
			sender_name: (val('auth.email.sender_name') as string) ?? '',
			accent: (val('auth.email.accent') as string) ?? null,
			footer: (val('auth.email.footer') as string) ?? '',
			logo: (val('auth.email.logo') as boolean | undefined) ?? true
		};
		if (reset === 'all' || reset === 'settings' || !sDirty) s = { ...next };
		sSaved = { ...next };
		const d: Record<string, Draft> = {};
		for (const t of EMAIL_TEMPLATES)
			d[t.id] = {
				subject: (val(subjectKey(t.id)) as string) ?? '',
				body: (val(bodyKey(t.id)) as string) ?? ''
			};
		const keep = EMAIL_TEMPLATES.filter(
			(t) => reset !== 'all' && reset !== t.id && drafts[t.id] && tDirty(t.id)
		);
		const nd = structuredClone(d);
		for (const t of keep) nd[t.id] = { ...drafts[t.id] };
		saved = structuredClone(d);
		drafts = nd;
	}

	async function load() {
		await stackConfig.load();
		fill();
		loaded = true;
	}
	onMount(() => {
		if (window.innerWidth < 700) device = 'mobile';
		load();
	});

	async function saveChange(c: ConfigChange, msg: string, what: string) {
		try {
			await stackConfig.save(c);
			fill(what);
			if (msg) toasts.push(msg);
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
		await saveChange({ set, unset }, 'Email settings saved.', 'settings');
		savingSettings = false;
	}
	const discardSettings = () => (s = { ...sSaved });

	/** The config change that saves an email's subject and message. */
	function templateChange(id: string): ConfigChange {
		const d = drafts[id];
		const set: Record<string, string> = {};
		const unset: string[] = [];
		if (d.subject.trim()) set[subjectKey(id)] = d.subject.trim();
		else if (saved[id].subject) unset.push(subjectKey(id));
		if (d.body.trim()) set[bodyKey(id)] = d.body;
		else if (saved[id].body) unset.push(bodyKey(id));
		return { set, unset };
	}

	async function saveTemplate() {
		savingTpl = true;
		await saveChange(templateChange(tplId), `${tpl.label} email saved.`, tplId);
		savingTpl = false;
	}

	async function resetTemplate() {
		savingTpl = true;
		const unset = [subjectKey(tplId), bodyKey(tplId)].filter((k) => key(k)?.set);
		if (unset.length) await saveChange({ unset }, `${tpl.label} email is back to Boson’s wording.`, tplId);
		else drafts[tplId] = { subject: '', body: '' };
		savingTpl = false;
	}

	beforeNavigate((nav) => {
		if (!dirty || savingSettings || savingTpl || savingMj) return;
		// Closing the tab or leaving the app: the browser asks.
		if (nav.type === 'leave') nav.cancel();
		else if (!confirm('Leave the Emails page? Your unsaved email changes will be lost.')) nav.cancel();
	});

	// ---- variable chips ----
	let bodyEl = $state<HTMLTextAreaElement>();
	let subjectEl = $state<HTMLInputElement>();
	let lastField = $state<'subject' | 'body'>('body');
	function insertVar(v: string) {
		const el = lastField === 'subject' ? subjectEl : bodyEl;
		if (!el) return;
		const field = lastField;
		const r = insertAt(drafts[tplId][field], el.selectionStart ?? 0, el.selectionEnd ?? 0, `{{${v}}}`);
		drafts[tplId][field] = r.value;
		requestAnimationFrame(() => {
			el.focus();
			el.setSelectionRange(r.caret, r.caret);
		});
	}

	// ---- live preview ----
	let preview = $state<EmailPreview | null>(null);
	let previewError = $state('');
	let previewOff = $state('');
	let previewing = $state(false);
	/** Validation errors from the last render, by input. */
	let fieldErrors = $state<Partial<Record<PreviewField, string>>>({});
	/** auth renders unsaved branding (it sent a from line). */
	let livebranding = $derived(!preview || brandingPreviews(preview));
	let previewMode = $state<'html' | 'text'>('html');
	let scheme = $state<'light' | 'dark'>('light');
	let device = $state<'desktop' | 'mobile'>('desktop');
	let frameWidth = $derived(device === 'desktop' ? 600 : 375);

	const lp = livePreview<PreviewRequest, EmailPreview>({
		fetch: (req, signal) => api<EmailPreview>('POST', '/emails/preview', req, undefined, signal),
		onResult(p) {
			preview = p;
			previewError = '';
			previewOff = '';
			fieldErrors = {};
		},
		onError(e) {
			if (isUnsupported(e)) {
				previewOff = e.message;
				return;
			}
			const f = e instanceof ApiError && e.status === 400 ? previewField(e.field) : undefined;
			if (f) {
				// Keep the last good preview; the message goes next to the input.
				fieldErrors = { [f]: e instanceof Error ? e.message : String(e) };
				previewError = '';
			} else {
				fieldErrors = {};
				previewError = errorText(e);
			}
		},
		onLoading: (b) => (previewing = b)
	});
	onDestroy(() => lp.cancel());

	let mjHtml = $derived(previewHTML(selected, selected === LAYOUT ? 'mjml' : modeOf(selected), compiled));
	let request = $derived<PreviewRequest>({
		...previewRequest(tplId, draft, s, DEFAULT_ACCENT),
		...mjHtml
	});
	let lastTemplate = '';
	let lastHtml = '';
	$effect(() => {
		const req = request;
		if (!loaded) return;
		if (!session.info?.features.email_preview) {
			previewOff = 'off';
			return;
		}
		untrack(() => {
			// Another email, or a compile that just finished (the worker
			// already waited for a pause): show it at once; typing in the
			// form: wait for a pause.
			const html = (req.layout_html ?? '') + '\u0000' + (req.html ?? '');
			if (req.template !== lastTemplate || html !== lastHtml) {
				lastTemplate = req.template;
				lastHtml = html;
				void lp.now(req);
			} else lp.schedule(req);
		});
	});
	const fieldError = (f: PreviewField) => fieldErrors[f];

	// The frame: two iframes take turns, so a new render loads out of sight
	// and replaces the old one only once it's laid out (no blank flash).
	let docs = $state<[string, string]>(['', '']);
	let front = $state(0);
	let loadedDocs = ['', ''];
	let frameHeight = $state(320);
	let frames: (HTMLIFrameElement | undefined)[] = $state([undefined, undefined]);
	let html = $derived(preview ? frameDoc(preview.html, scheme) : '');
	$effect(() => {
		const h = html;
		untrack(() => {
			if (!h || docs[front] === h) return;
			const back = 1 - front;
			if (docs[back] === h && loadedDocs[back] === h) swap(back);
			else docs[back] = h;
		});
	});
	let ro: ResizeObserver | undefined;
	function measure(i: number) {
		const d = frames[i]?.contentDocument;
		if (!d?.documentElement) return 0;
		return Math.ceil(d.documentElement.getBoundingClientRect().height);
	}
	function swap(i: number) {
		front = i;
		const h = measure(i);
		if (h) frameHeight = Math.max(160, h);
		ro?.disconnect();
		const d = frames[i]?.contentDocument;
		if (d?.documentElement && typeof ResizeObserver !== 'undefined') {
			ro = new ResizeObserver(() => {
				const h = measure(front);
				if (h) frameHeight = Math.max(160, h);
			});
			ro.observe(d.documentElement);
		}
	}
	function frameLoaded(i: number) {
		const d = docs[i];
		loadedDocs[i] = d;
		if (d && d === html) swap(i);
	}
	onDestroy(() => ro?.disconnect());

	/** The stage scales the frame down to fit a narrow column. */
	let stageWidth = $state(0);
	let scale = $derived(stageWidth && stageWidth < frameWidth ? stageWidth / frameWidth : 1);

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
				template: tplId,
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
	let testDirty = $derived(tDirty(tplId) || sDirty || mjDirty);
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
	<div class="layout" class:wide={!!activePart}>
		<div class="col">
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
							aria-invalid={!!fieldError('sender_name')}
						/>
						{#if fieldError('sender_name')}<small class="err" role="alert">{fieldError('sender_name')}</small>
						{:else}<small>Shown as who the email is from. Empty: {senderDefault}.</small>{/if}
					</label>
					<label class="field">
						<span>Footer</span>
						<input
							type="text"
							bind:value={s.footer}
							maxlength="200"
							placeholder="Example Chat · example.com"
							disabled={!editable}
							aria-invalid={!!fieldError('footer')}
						/>
						{#if fieldError('footer')}<small class="err" role="alert">{fieldError('footer')}</small>
						{:else}<small>One line under every email.</small>{/if}
					</label>
					<div class="field">
						<span id="accent-l">Accent</span>
						<div class="accent">
							<span class="chip" style:--c={s.accent ?? DEFAULT_ACCENT} aria-hidden="true"></span>
							<code>{s.accent ?? DEFAULT_ACCENT}</code>
							{#if !s.accent}<span class="muted small">default</span>{/if}
						</div>
						<ColorPicker bind:value={s.accent} labelledby="accent-l" disabled={!editable} />
						{#if fieldError('accent')}<small class="err" role="alert">{fieldError('accent')}</small>
						{:else}<small>Buttons and links. “No colour” uses auth’s default blue.</small>{/if}
					</div>
					<div class="field">
						<span>Logo</span>
						<Toggle
							label="Put the logo at the top"
							detail="The wordmark if there is one, else the square logo, from the Branding page."
							bind:checked={s.logo}
							disabled={!editable}
						/>
						{#if fieldError('logo')}<small class="err" role="alert">{fieldError('logo')}</small>{/if}
					</div>
				</div>
				<div class="row-end">
					<button
						class="btn btn-ghost"
						type="button"
						disabled={!sDirty || savingSettings}
						onclick={discardSettings}>Discard</button
					>
					<button
						class="btn btn-primary"
						type="button"
						disabled={(!sDirty && branded) || savingSettings || !editable}
						onclick={saveSettings}>{savingSettings ? 'Saving…' : 'Save settings'}</button
					>
				</div>
			</section>

			<section class="card" aria-labelledby="tpl-h">
				<h2 id="tpl-h" class="sr-only">Messages</h2>
				<div class="tabs" role="tablist" aria-label="Email">
					<button
						type="button"
						role="tab"
						id="tab-layout"
						aria-selected={selected === LAYOUT}
						aria-controls="tpl-panel"
						onclick={() => (selected = LAYOUT)}
					>
						Layout (MJML)
						{#if isDirty(mj[LAYOUT])}<span class="dot unsaved" title="Unsaved"></span>
						{:else if hasOverride(LAYOUT)}<span class="dot edited" title="Custom layout"></span>{/if}
					</button>
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
							{#if partDirty(t.id)}<span class="dot unsaved" title="Unsaved"></span>
							{:else if custom(t.id) || hasOverride(t.id)}<span class="dot edited" title="Customised"
								></span>{/if}
						</button>
					{/each}
				</div>
				<div class="panel" id="tpl-panel" role="tabpanel" aria-labelledby="tab-{selected}">
					{#if selected === LAYOUT}
						<p class="hint">
							The frame every email is wrapped in, as <a
								href="https://mjml.io"
								target="_blank"
								rel="noreferrer">MJML</a
							>. <code>{'{{content}}'}</code> is where each email’s text and button go. It’s compiled in your browser;
							auth sends the HTML. An email with its own MJML doesn’t use the layout.
						</p>
						<div class="field inline">
							<span id="pvwith-l">Preview with</span>
							<Dropdown
								options={EMAIL_TEMPLATES.map((t) => ({ value: t.id, label: t.label }))}
								bind:value={previewTpl}
								labelledby="pvwith-l"
								size="small"
							/>
						</div>
						{@render mjmlEditor(LAYOUT)}
					{:else}
						<div class="mode-row">
							<p class="hint">{tpl.detail}</p>
							<div class="seg" role="group" aria-label="Edit as">
								<button
									type="button"
									aria-pressed={modeOf(selected) === 'text'}
									onclick={() => (modes[selected] = 'text')}>Text</button
								>
								<button
									type="button"
									aria-pressed={modeOf(selected) === 'mjml'}
									onclick={() => (modes[selected] = 'mjml')}>MJML</button
								>
							</div>
						</div>
						<label class="field">
							<span>Subject</span>
							<input
								bind:this={subjectEl}
								type="text"
								bind:value={drafts[tplId].subject}
								onfocus={() => (lastField = 'subject')}
								maxlength={MAX_SUBJECT}
								placeholder="Boson’s default subject"
								disabled={!editable}
								aria-invalid={!!fieldError('subject')}
							/>
							{#if fieldError('subject')}<small class="err" role="alert">{fieldError('subject')}</small>{/if}
						</label>
						{#if modeOf(selected) === 'mjml'}
							{@render mjmlEditor(selected)}
						{:else}
							{#if hasOverride(selected)}
								<Notice tone="info">
									This email has its own MJML, so the layout and the message below aren’t used for its HTML.
									The message is still its plain-text part. Switch to MJML to edit it or go back to the
									layout.
								</Notice>
							{/if}
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
									bind:value={drafts[tplId].body}
									onfocus={() => (lastField = 'body')}
									rows="11"
									maxlength={MAX_BODY}
									placeholder="Boson’s default wording. Type to replace it. Blank lines start paragraphs; links become buttons."
									disabled={!editable}
									aria-invalid={!!fieldError('body')}></textarea>
								{#if fieldError('body')}<small class="err" role="alert">{fieldError('body')}</small>
								{:else}<small
										>Plain text. A blank line starts a new paragraph and links are made clickable. {draft.body
											.length}/{MAX_BODY}</small
									>{/if}
							</label>
							{#if unknown.length}
								<Notice tone="warn">
									{unknown.map((u) => `{{${u}}}`).join(', ')}
									{unknown.length === 1 ? 'isn’t' : 'aren’t'} filled in for this email and will show as typed.
								</Notice>
							{/if}
							<div class="row-end">
								{#if custom(tplId) || draft.subject || draft.body}
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
									disabled={!tDirty(tplId) || savingTpl || !editable}
									onclick={saveTemplate}>{savingTpl ? 'Saving…' : 'Save email'}</button
								>
							</div>
						{/if}
					{/if}
				</div>
			</section>
		</div>

		{#snippet mjmlEditor(part: string)}
			{@const d = mj[part]}
			<div class="field">
				<span id="ph-l">Insert</span>
				<div class="chips" role="group" aria-labelledby="ph-l">
					{#each placeholdersFor(part) as v (v)}
						<button
							type="button"
							class="var"
							title={PLACEHOLDER_HELP[v]}
							disabled={!editable || !d}
							onmousedown={(e) => e.preventDefault()}
							onclick={() => editor?.insert(`{{${v}}}`)}>{`{{${v}}}`}</button
						>
					{/each}
				</div>
			</div>
			{#if mjLoadError[part]}
				<Notice tone="bad">{mjLoadError[part]}</Notice>
			{:else if !d}
				<p class="muted small">Loading the MJML…</p>
			{:else}
				<div class="field">
					<div class="ed-head">
						<span id="mj-l">{part === LAYOUT ? 'Layout MJML' : 'Email MJML'}</span>
						<span class="ed-status" data-testid="mjml-status" aria-live="polite">
							{#if compileError}
								<span class="bad">Compiler unavailable</span>
							{:else if !current || (compiling && !upToDate)}
								Compiling…
							{:else if errorCount}
								<span class="bad">{errorCount} {errorCount === 1 ? 'error' : 'errors'}</span>{warnCount
									? `, ${warnCount} ${warnCount === 1 ? 'warning' : 'warnings'}`
									: ''}
							{:else if warnCount}
								<span class="warn">{warnCount} {warnCount === 1 ? 'warning' : 'warnings'}</span>
							{:else}
								<span class="ok">Compiled</span> · {sizeKB(current.html)} KB of 512
							{/if}
						</span>
					</div>
					{#key part}
						<MjmlEditor
							bind:this={editor}
							value={d.draft}
							{diagnostics}
							readonly={!editable}
							label={part === LAYOUT ? 'Layout MJML' : `${partLabel(part)} email MJML`}
							onchange={(v) => setDraft(part, v)}
						/>
					{/key}
					{#if compileError}
						<small class="err" role="alert">{compileError}</small>
					{/if}
					{#if mjError || fieldError('mjml')}
						<small class="err" role="alert">{mjError || fieldError('mjml')}</small>
					{/if}
					{#if diagnostics.length}
						<ul class="problems" aria-label="MJML problems" data-testid="mjml-problems">
							{#each diagnostics as p, i (i)}
								<li class={p.severity}>
									<button
										type="button"
										class="ln"
										title="Go to line {p.line}"
										onclick={() => editor?.goToLine(p.line)}>Line {p.line}</button
									>
									<span class="sev">{p.severity}</span>
									<span class="msg">{p.message}</span>
								</li>
							{/each}
						</ul>
					{:else if part !== LAYOUT}
						<small
							>The whole email is yours: it isn’t wrapped in the layout. It must contain <code
								>{'{{link}}'}</code
							>. The plain-text part is the Text tab’s message, or text made from this HTML.</small
						>
					{/if}
				</div>
				<div class="row-end wrap">
					{#if hasOverride(part)}
						<button type="button" class="btn btn-ghost" disabled={savingMj || !editable} onclick={removeMjml}
							>{part === LAYOUT ? 'Use Boson’s layout' : 'Remove own MJML'}</button
						>
					{/if}
					<button
						type="button"
						class="btn btn-ghost"
						disabled={savingMj || !editable}
						onclick={resetMjml}
						title="Put Boson’s MJML in the editor (not saved)">Reset to default</button
					>
					{#if isDirty(d)}
						<button type="button" class="btn btn-ghost" disabled={savingMj} onclick={discardMjml}
							>Discard</button
						>
					{/if}
					<button
						class="btn btn-primary"
						type="button"
						disabled={!editable ||
							savingMj ||
							!(isDirty(d) || (part !== LAYOUT && (tDirty(part) || !hasOverride(part)))) ||
							(upToDate && !current?.ok)}
						onclick={saveMjml}>{savingMj ? 'Saving…' : part === LAYOUT ? 'Save layout' : 'Save email'}</button
					>
				</div>
			{/if}
		{/snippet}

		<section class="card preview" aria-labelledby="pv-h">
			<div class="pv-head">
				<h2 id="pv-h">Preview</h2>
				<span class="live" aria-live="polite"
					>{previewing ? 'Updating…' : dirty ? 'Unsaved changes' : 'Saved'}</span
				>
			</div>
			<div class="controls">
				<div class="seg" role="group" aria-label="Preview width">
					<button type="button" aria-pressed={device === 'desktop'} onclick={() => (device = 'desktop')}
						>Desktop</button
					>
					<button type="button" aria-pressed={device === 'mobile'} onclick={() => (device = 'mobile')}
						>Mobile</button
					>
				</div>
				<div class="seg" role="group" aria-label="Mail client colours">
					<button type="button" aria-pressed={scheme === 'light'} onclick={() => (scheme = 'light')}
						>Light</button
					>
					<button type="button" aria-pressed={scheme === 'dark'} onclick={() => (scheme = 'dark')}
						>Dark</button
					>
				</div>
				<div class="seg" role="tablist" aria-label="Preview format">
					<button
						type="button"
						role="tab"
						aria-selected={previewMode === 'html'}
						onclick={() => (previewMode = 'html')}>HTML</button
					>
					<button
						type="button"
						role="tab"
						aria-selected={previewMode === 'text'}
						onclick={() => (previewMode = 'text')}>Text</button
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
			{:else}
				{#if previewError}
					<Notice tone="bad">{previewError}</Notice>
				{/if}
				{#if preview}
					<div class="mail" class:busy={previewing} aria-busy={previewing}>
						<div class="loading" aria-hidden="true"></div>
						<dl class="envelope" data-testid="envelope">
							{#if preview.from}
								<dt>From</dt>
								<dd data-testid="pv-from">{preview.from}</dd>
							{/if}
							<dt>Subject</dt>
							<dd class="subj" data-testid="pv-subject">{preview.subject}</dd>
						</dl>
						{#if previewMode === 'html'}
							<div class="stage {scheme}" bind:clientWidth={stageWidth}>
								<div class="fit" style:width="{frameWidth * scale}px" style:height="{frameHeight * scale}px">
									{#each [0, 1] as i (i)}
										<iframe
											bind:this={frames[i]}
											class="frame"
											class:back={i !== front}
											title={i === front ? 'Email preview' : 'Email preview (loading)'}
											aria-hidden={i !== front}
											sandbox="allow-same-origin"
											referrerpolicy="no-referrer"
											srcdoc={docs[i]}
											style:width="{frameWidth}px"
											style:height="{frameHeight}px"
											style:transform={scale < 1 ? `scale(${scale})` : undefined}
											style:color-scheme={scheme}
											onload={() => frameLoaded(i)}
										></iframe>
									{/each}
								</div>
							</div>
						{:else}
							<pre class="text" data-testid="pv-text">{preview.text}</pre>
						{/if}
					</div>
					{#if !livebranding}
						<p class="hint note" data-testid="legacy-note">
							Branding changes preview after saving (update auth).
						</p>
					{/if}
					{#if preview.warnings?.length}
						<Notice tone="warn">
							<ul class="warnings" data-testid="pv-warnings">
								{#each preview.warnings as w, i (i)}<li>{w}</li>{/each}
							</ul>
						</Notice>
					{/if}
					<p class="hint">
						Example values fill in the variables.{previewMode === 'html' && scale < 1
							? ` Shown at ${Math.round(scale * 100)}% to fit; the email is ${frameWidth} px wide.`
							: ''}
					</p>
				{:else if !previewError}
					<p class="muted small">Rendering…</p>
				{/if}
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
					title={testDirty ? 'Sends the saved version' : undefined}
					>{sending ? 'Sending…' : 'Send test'}</button
				>
			</form>
			{#if testDirty}
				<p class="hint">The test uses what’s saved; save first to send your edits.</p>
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
	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
		gap: var(--space-5) var(--space-6);
	}
	.err {
		color: var(--danger-text) !important;
	}
	[aria-invalid='true'] {
		border-color: var(--danger);
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
		grid-template-columns: minmax(0, 5fr) minmax(0, 6fr);
		gap: var(--space-6);
		align-items: start;
	}
	/* an MJML editor wants the room: half and half */
	.layout.wide {
		grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
	}
	.col {
		display: grid;
		gap: var(--space-6);
		min-width: 0;
	}
	.preview {
		position: sticky;
		top: var(--space-6);
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
		gap: var(--space-3);
	}
	.live {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.controls {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
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
	.seg button[aria-selected='true'],
	.seg button[aria-pressed='true'] {
		background: var(--surface-raised);
		color: var(--fg);
		box-shadow: inset 0 0 0 1px var(--border-strong);
	}
	.mail {
		position: relative;
		display: grid;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		overflow: hidden;
		background: var(--bg);
	}
	/* a thin bar sweeps along the top while a new render loads */
	.loading {
		position: absolute;
		inset: 0 0 auto;
		height: 2px;
		z-index: 2;
		opacity: 0;
		background: linear-gradient(90deg, transparent, var(--accent), transparent);
		background-size: 40% 100%;
		background-repeat: no-repeat;
		transition: opacity 0.15s;
	}
	.busy .loading {
		opacity: 1;
		animation: sweep 0.9s linear infinite;
	}
	@keyframes sweep {
		from {
			background-position: -40% 0;
		}
		to {
			background-position: 140% 0;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.busy .loading {
			animation: none;
			background: var(--accent);
			opacity: 0.5;
		}
	}
	.envelope {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr);
		gap: var(--space-1) var(--space-3);
		margin: 0;
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--border);
		background: var(--surface-raised);
		font-size: var(--text-sm);
	}
	.envelope dt {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
		text-transform: uppercase;
		letter-spacing: var(--tracking-label);
		line-height: 1.6;
	}
	.envelope dd {
		margin: 0;
		color: var(--fg-2);
		overflow-wrap: anywhere;
	}
	.envelope .subj {
		color: var(--fg);
		font-weight: 600;
	}
	/* the mail client's canvas around the message */
	.stage {
		display: flex;
		justify-content: center;
		padding: var(--space-4) 0;
		background: #f1f2f4;
		min-width: 0;
	}
	.stage.dark {
		background: #121214;
	}
	.fit {
		position: relative;
		flex: none;
		overflow: hidden;
		transition: height 0.12s ease-out;
	}
	.frame {
		position: absolute;
		top: 0;
		left: 0;
		border: 0;
		display: block;
		transform-origin: 0 0;
		background: transparent;
	}
	.frame.back {
		visibility: hidden;
		pointer-events: none;
	}
	.text {
		margin: 0;
		min-height: 200px;
		max-height: 560px;
		overflow: auto;
		padding: var(--space-4);
		background: var(--bg);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		white-space: pre-wrap;
		word-break: break-word;
	}
	.busy .stage,
	.busy .text {
		opacity: 0.85;
		transition: opacity 0.2s;
	}
	.note {
		color: var(--warn-text);
	}
	.test {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: var(--space-3);
		align-items: end;
		padding-top: var(--space-4);
		border-top: 1px solid var(--border-soft);
	}
	.mode-row {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-3);
	}
	.mode-row .seg {
		flex: none;
	}
	.field.inline {
		display: flex;
		align-items: center;
		gap: var(--space-3);
	}
	.field.inline :global(.dd) {
		width: auto;
		min-width: 200px;
	}
	.ed-head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-3);
		flex-wrap: wrap;
	}
	.ed-status {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.ed-status .ok {
		color: var(--success-text);
	}
	.ed-status .bad {
		color: var(--danger-text);
	}
	.ed-status .warn {
		color: var(--warn-text);
	}
	.problems {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		max-height: 168px;
		overflow: auto;
		font-size: var(--text-sm);
	}
	.problems li {
		display: grid;
		grid-template-columns: auto auto minmax(0, 1fr);
		gap: var(--space-2);
		align-items: baseline;
		padding: 6px var(--space-3);
		border-left: 2px solid var(--danger);
		background: var(--surface-raised);
	}
	.problems li + li {
		border-top: 1px solid var(--border-soft);
	}
	.problems li.warning {
		border-left-color: var(--warn);
	}
	.problems .ln {
		border: 0;
		background: none;
		padding: 0;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--fg);
		cursor: pointer;
		text-decoration: underline;
		text-decoration-color: var(--border-strong);
		text-underline-offset: 3px;
		white-space: nowrap;
	}
	.problems .ln:hover {
		text-decoration-color: currentColor;
	}
	.problems .sev {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: var(--tracking-label);
		color: var(--danger-text);
	}
	.problems .warning .sev {
		color: var(--warn-text);
	}
	.problems .msg {
		color: var(--fg-2);
		overflow-wrap: anywhere;
	}
	.row-end.wrap {
		flex-wrap: wrap;
	}
	.warnings {
		margin: 0;
		padding-left: 1.1em;
	}
	@media (max-width: 1100px) {
		.layout,
		.layout.wide {
			grid-template-columns: minmax(0, 1fr);
		}
		.preview {
			position: static;
		}
	}
</style>
