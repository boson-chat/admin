<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { stackConfig } from '$lib/stackconfig.svelte';
	import type { ConfigServiceStatus } from '$lib/types';
	import {
		DEFAULT_PORT,
		DEFAULT_RATE,
		DOCS_URL,
		KEYS,
		MAX_RATE,
		changeCount,
		changes,
		draftFrom,
		exampleAddress,
		otherServer,
		passwordState,
		testBody,
		validate,
		workerState,
		type Draft,
		type Field,
		type NickClaimTestResult,
		type NickClaimView,
		type Source
	} from '$lib/nickclaim';
	import ConfigStatus from '$lib/components/ConfigStatus.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import State from '$lib/components/State.svelte';
	import TestResult from '$lib/components/TestResult.svelte';
	import Time from '$lib/components/Time.svelte';
	import Toggle from '$lib/components/Toggle.svelte';

	let view = $derived(stackConfig.view);
	let nick = $state<NickClaimView | null>(null);
	let loaded = $state(false);
	let refreshing = $state(false);
	let saving = $state(false);
	let checking = $state(false);
	let testing = $state(false);
	let result = $state<NickClaimTestResult | null>(null);
	let testError = $state('');
	let testPassword = $state('');
	/** An error the BFF put on one field ("nick_claim.inbound.host"). */
	let serverError = $state<{ field: Field; message: string } | null>(null);

	let saved = $state<Draft | null>(null);
	let sources = $state<Record<Field, Source> | null>(null);
	let draft = $state<Draft | null>(null);

	let status = $derived(nick?.status ?? null);
	let editable = $derived(!!view?.keys.find((k) => k.key === KEYS.enabled)?.editable);
	let configOff = $derived(session.info?.features.config_file === false || view?.enabled === false);
	let errors = $derived(draft ? validate(draft) : {});
	let shownErrors = $derived.by(() => {
		const out: Partial<Record<Field, string>> = { ...errors };
		if (serverError && !out[serverError.field]) out[serverError.field] = serverError.message;
		return out;
	});
	let hasErrors = $derived(Object.keys(errors).length > 0);
	let change = $derived(draft && saved ? changes(draft, saved, view) : {});
	let count = $derived(changeCount(change));
	let dirty = $derived(count > 0);
	let password = $derived(passwordState(status));
	let worker = $derived(workerState(status));
	let needsTestPassword = $derived(!!draft && otherServer(draft, status));
	let apiStatus = $derived<ConfigServiceStatus[]>(
		(stackConfig.statuses ?? []).filter((s) => s.name === 'api')
	);

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
			nick = await api<NickClaimView>('GET', '/nick-claims');
		} catch (e) {
			nick = {
				configured: true,
				password_env: 'NICK_CLAIM_POP3_PASSWORD',
				error: errorText(e),
				checked_at: new Date().toISOString()
			};
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
			// Values the form takes from the api follow it while nothing is edited.
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

	// ---- saving ----
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
			if (!stackConfig.statuses?.some((s) => s.name === 'api' && s.state === 'pending')) break;
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
			toasts.push('Nick registration saved. The api picks it up within seconds.');
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

	// ---- test ----
	async function test() {
		if (!draft) return;
		testing = true;
		result = null;
		testError = '';
		try {
			result = await api<NickClaimTestResult>(
				'POST',
				'/nick-claims/test',
				testBody(draft, needsTestPassword ? testPassword : '')
			);
		} catch (e) {
			testError = errorText(e);
		} finally {
			testing = false;
		}
	}
	// A result is about the values it tested.
	let tested = $derived(JSON.stringify([draft?.host, draft?.port, draft?.tls, draft?.user]));
	$effect(() => {
		void tested;
		result = null;
		testError = '';
	});

	const workerWord: Record<string, [string, string]> = {
		off: ['Off', ''],
		running: ['Running', 'tag-ok'],
		failing: ['Failing', 'tag-bad'],
		stopped: ['Stopped', 'tag-warn']
	};
	const envNames: Partial<Record<Field, string>> = {
		email_domain: 'NICK_CLAIM_EMAIL_DOMAIN',
		host: 'POP3_HOST',
		port: 'POP3_PORT',
		tls: 'POP3_TLS',
		user: 'POP3_USER'
	};
	const errId = (f: Field) => `nc-${f}-err`;
</script>

<svelte:head><title>Nick registration · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Nick registration</h1>
		<p>
			Boson can register your users’ nicks on other IRC networks automatically by confirming NickServ emails
			sent to a catch-all address. Needs a domain with catch-all mail (or plus-addressing) and a POP3 mailbox.
		</p>
		<p class="docs">
			<a href={DOCS_URL} target="_blank" rel="noreferrer">Setup guide: docs/nick-claim.md</a>
			<span class="muted">mail setup, privacy notes</span>
		</p>
	</div>
</div>

{#if configOff}
	<State kind="off" title="Nick registration lives in the stack config file">
		Set <code>BOSON_CONFIG=/config/boson.yaml</code> on the admin service and mount the stack’s
		<code>config/</code> directory read-write. The api reads the same file’s <code>nick_claim:</code> section.
	</State>
{:else if !loaded}
	<State kind="loading" title="Loading nick registration…" />
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
		{#if nick && !nick.configured}
			<Notice tone="warn">
				<strong>The api service isn’t reachable from the admin panel.</strong> You can still save these
				settings, but the panel can’t show the worker’s status or test the mailbox. Set
				<code>BOSON_API_INTERNAL_URL=http://api:3001</code> and <code>BOSON_API_INTERNAL_TOKEN</code> (the api’s
				internal token) on the admin service.
			</Notice>
		{:else if nick?.error}
			<Notice tone="bad">Couldn’t read the api’s status: {nick.error}</Notice>
		{/if}
		{#if status?.config_error}
			<Notice tone="bad">
				The api rejected its nick registration settings and keeps the feature off: {status.config_error}
			</Notice>
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
				<legend class="sr-only">Nick registration settings</legend>
				<section class="card switch" class:on={draft.enabled} aria-labelledby="nc-on-h">
					<h2 id="nc-on-h" class="sr-only">Switch</h2>
					<Toggle
						label="Register nicks on other networks"
						detail="Boson app users get a one-tap “register my nick” on networks like Libera or OFTC. Off: the app hides the option and the mailbox is never touched."
						bind:checked={draft.enabled}
					/>
					{#if draft.enabled && (errors.email_domain || errors.host)}
						<p class="err" role="alert">
							Add {[errors.email_domain && 'an email domain', errors.host && 'a mail server']
								.filter(Boolean)
								.join(' and ')}
							below to turn it on.
						</p>
					{/if}
				</section>

				<section class="group" aria-labelledby="nc-addr-h">
					<div class="group-head">
						<h2 id="nc-addr-h">Addresses</h2>
						<p>The one-off address Boson gives each network’s NickServ, and how often a user may ask.</p>
					</div>
					<div class="card fields">
						<div class="field">
							<label class="lbl" for="nc-domain">Email domain</label>
							<input
								id="nc-domain"
								type="text"
								bind:value={draft.email_domain}
								placeholder="nick.example.com"
								autocomplete="off"
								spellcheck="false"
								aria-invalid={!!shownErrors.email_domain}
								aria-describedby="nc-domain-hint{shownErrors.email_domain ? ' ' + errId('email_domain') : ''}"
							/>
							{#if shownErrors.email_domain}<p class="err" id={errId('email_domain')}>
									{shownErrors.email_domain}
								</p>{/if}
							<small id="nc-domain-hint">
								Addresses look like <code class="addr">{exampleAddress(draft.email_domain)}</code>. Its MX
								must deliver them to the mailbox below.
								{#if sources?.email_domain === 'env' && draft.email_domain === saved?.email_domain}
									From the api’s <code>{envNames.email_domain}</code>.{/if}
							</small>
						</div>
						<div class="field rate">
							<label class="lbl" for="nc-rate">Claims per user per hour</label>
							<input
								id="nc-rate"
								type="number"
								inputmode="numeric"
								min="1"
								max={MAX_RATE}
								step="1"
								bind:value={draft.rate_limit_per_hour}
								placeholder={String(DEFAULT_RATE)}
								aria-invalid={!!shownErrors.rate_limit_per_hour}
								aria-describedby="nc-rate-hint{shownErrors.rate_limit_per_hour
									? ' ' + errId('rate_limit_per_hour')
									: ''}"
							/>
							{#if shownErrors.rate_limit_per_hour}<p class="err" id={errId('rate_limit_per_hour')}>
									{shownErrors.rate_limit_per_hour}
								</p>{/if}
							<small id="nc-rate-hint"
								>More gets “try later”. Empty: <code>NICK_CLAIM_RATE_LIMIT_PER_HOUR</code>
								({DEFAULT_RATE}).</small
							>
						</div>
					</div>
				</section>

				<section class="group" aria-labelledby="nc-in-h">
					<div class="group-head">
						<h2 id="nc-in-h">Inbound mailbox (POP3)</h2>
						<p>
							Where the confirmation emails land. A plain <code>reg@</code> mailbox works when your provider
							supports plus-addressing (<code>reg+anything@</code> delivered to <code>reg@</code>); a
							catch-all isn’t required.
						</p>
					</div>
					<div data-testid="dedicated-warning">
						<Notice tone="warn">
							<strong>Use a mailbox dedicated to this.</strong> The worker deletes every message that doesn’t match
							a pending claim.
						</Notice>
					</div>
					<div class="card fields">
						<div class="row2">
							<div class="field">
								<label class="lbl" for="nc-host">Mail server</label>
								<input
									id="nc-host"
									type="text"
									bind:value={draft.host}
									placeholder="pop3.example.com"
									autocomplete="off"
									spellcheck="false"
									aria-invalid={!!shownErrors.host}
									aria-describedby={shownErrors.host ? errId('host') : undefined}
								/>
								{#if shownErrors.host}<p class="err" id={errId('host')}>{shownErrors.host}</p>{/if}
							</div>
							<div class="field">
								<label class="lbl" for="nc-port">Port</label>
								<input
									id="nc-port"
									type="number"
									inputmode="numeric"
									min="1"
									max="65535"
									step="1"
									bind:value={draft.port}
									placeholder={String(DEFAULT_PORT)}
									aria-invalid={!!shownErrors.port}
									aria-describedby={shownErrors.port ? errId('port') : undefined}
								/>
								{#if shownErrors.port}<p class="err" id={errId('port')}>{shownErrors.port}</p>{/if}
							</div>
						</div>
						<div class="tls">
							<Toggle
								label="TLS"
								detail="Encrypt the connection (POP3S, usually port 995). Turn off only for a server on a trusted network (port 110)."
								bind:checked={draft.tls}
							/>
						</div>
						<div class="field">
							<label class="lbl" for="nc-user">Username</label>
							<input
								id="nc-user"
								type="text"
								bind:value={draft.user}
								placeholder="reg@nick.example.com"
								autocomplete="off"
								spellcheck="false"
								aria-invalid={!!shownErrors.user}
								aria-describedby={shownErrors.user ? errId('user') : undefined}
							/>
							{#if shownErrors.user}<p class="err" id={errId('user')}>{shownErrors.user}</p>{/if}
						</div>
						<div class="field">
							<span id="nc-pw-l">Password</span>
							<div class="pw" aria-labelledby="nc-pw-l" data-testid="password-state" data-state={password}>
								{#if password === 'set'}
									<span class="tag tag-ok">Set in .env</span>
								{:else if password === 'missing'}
									<span class="tag tag-bad">Missing</span>
								{:else}
									<span class="tag">Unknown</span>
								{/if}
								<code>{nick?.password_env ?? 'NICK_CLAIM_POP3_PASSWORD'}</code>
							</div>
							<small>
								{#if password === 'missing'}
									Add <code>{nick?.password_env}=…</code> to the stack’s <code>.env</code> and run
									<code>docker compose up -d api</code>. It never goes in the config file.
								{:else if password === 'set'}
									A secret, so it stays in the api’s <code>.env</code>; the panel never sees it.
								{:else}
									Kept in the api’s <code>.env</code>, never in the config file. This api release doesn’t say
									whether it’s set.
								{/if}
							</small>
						</div>

						<div class="test">
							{#if needsTestPassword}
								<div class="field">
									<label class="lbl" for="nc-testpw">Password for this test</label>
									<input
										id="nc-testpw"
										type="password"
										bind:value={testPassword}
										autocomplete="new-password"
										aria-describedby="nc-testpw-hint"
									/>
									<small id="nc-testpw-hint">
										The api only reuses its stored password with <code
											>{status?.inbound.host || 'the server it uses'}</code
										>. For this server, type it here; it’s sent once and not saved.
									</small>
								</div>
							{/if}
							<div class="test-row">
								<button
									class="btn"
									type="button"
									onclick={test}
									disabled={testing ||
										!nick?.configured ||
										!!errors.host ||
										!!errors.port ||
										!draft.host.trim()}>{testing ? 'Testing…' : 'Test connection'}</button
								>
								<span class="small muted">
									{#if !nick?.configured}
										Needs the api’s internal URL.
									{:else if !draft.host.trim()}
										Enter a mail server first.
									{:else}
										Only signs in and counts the messages, with the values above (saved or not). It never
										reads or deletes anything.
									{/if}
								</span>
							</div>
							<div aria-live="polite">
								{#if result}
									<TestResult ok={result.ok} details={result.message} />
								{:else if testError}
									<TestResult ok={false} details={testError} />
								{/if}
							</div>
						</div>
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
						Saved to <code>nick_claim:</code> in config/boson.yaml
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

		<aside class="side" aria-labelledby="nc-st-h">
			<div class="card status" data-worker={worker}>
				<div class="st-head">
					<h2 id="nc-st-h">Status</h2>
					<button
						class="btn btn-small btn-ghost"
						type="button"
						onclick={refresh}
						disabled={refreshing || !nick?.configured}>{refreshing ? 'Checking…' : 'Refresh'}</button
					>
				</div>
				{#if status}
					<dl>
						<dt>Feature</dt>
						<dd>
							{#if status.enabled}<span class="tag tag-accent">On</span>{:else}<span class="tag">Off</span
								>{/if}
						</dd>
						<dt>Worker</dt>
						<dd>
							<span class="tag {workerWord[worker][1]}" data-testid="worker-state"
								><span class="dot" aria-hidden="true"></span>{workerWord[worker][0]}</span
							>
						</dd>
						<dt>Last check</dt>
						<dd><Time at={status.worker.last_drain_at} empty="Never" /></dd>
						<dt>Last OK</dt>
						<dd><Time at={status.worker.last_ok_at} empty="Never" /></dd>
						<dt>Failures in a row</dt>
						<dd class:bad={(status.worker.consecutive_failures ?? 0) > 0}>
							{status.worker.consecutive_failures ?? 0}
						</dd>
						<dt>Pending claims</dt>
						<dd>{status.pending_claims}</dd>
						<dt>Last error</dt>
						<dd class="last-err" class:bad={!!status.worker.last_error}>
							{status.worker.last_error || 'None'}
						</dd>
					</dl>
					<p class="checked small muted">Checked <Time at={nick?.checked_at} /> · every 15 s</p>
				{:else}
					<p class="small muted none">
						{nick && !nick.configured
							? 'No status: the api service isn’t reachable from the admin panel.'
							: 'No status from the api right now.'}
					</p>
				{/if}
			</div>
			{#if apiStatus.length}
				<ConfigStatus statuses={apiStatus} {checking} onrecheck={recheck} />
			{/if}
		</aside>
	</div>
{/if}

<style>
	/* the global .field > span label look, on a real <label> */
	.lbl {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-quiet);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
	}
	.docs {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
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
		display: grid;
		gap: var(--space-3);
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
	.rate input {
		max-width: 10rem;
	}
	.row2 {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 8rem;
		gap: var(--space-4);
	}
	.tls {
		padding: var(--space-3) 0;
		border-top: 1px solid var(--border-soft);
		border-bottom: 1px solid var(--border-soft);
	}
	.field small code,
	.addr {
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
	.pw {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		min-height: 38px;
	}
	.pw code {
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
	}
	.test {
		display: grid;
		gap: var(--space-3);
		padding-top: var(--space-4);
		border-top: 1px solid var(--border-soft);
	}
	.test-row {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-4);
		align-items: center;
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
	}
	dd.bad {
		color: var(--danger-text);
	}
	.last-err {
		grid-column: 1 / -1;
		font-size: var(--text-xs);
		line-height: 1.5;
		overflow-wrap: anywhere;
		color: var(--fg-2);
	}
	dt:nth-last-of-type(1) {
		grid-column: 1 / -1;
		margin-bottom: calc(-1 * var(--space-1));
	}
	[data-worker='running'] .dot {
		background: var(--success);
	}
	[data-worker='failing'] .dot {
		background: var(--danger);
	}
	[data-worker='stopped'] .dot {
		background: var(--warn);
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
	@media (max-width: 480px) {
		.row2 {
			grid-template-columns: minmax(0, 1fr) 6.5rem;
		}
	}
</style>
