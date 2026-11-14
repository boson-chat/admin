<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { datetime } from '$lib/format';
	import type {
		DirectoryPeer,
		DirectoryServer,
		DirectoryStatus,
		Federation,
		Listing,
		PeerCheck,
		PeerSyncResult,
		VerifyResult
	} from '$lib/types';
	import Confirm from '$lib/components/Confirm.svelte';
	import CopyField from '$lib/components/CopyField.svelte';
	import Notice from '$lib/components/Notice.svelte';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import { stackConfig } from '$lib/stackconfig.svelte';

	let st = $state<DirectoryStatus | null>(null);
	let error = $state('');
	let off = $derived(session.info ? !session.info.features.directory : false);

	// form
	let name = $state('');
	let description = $state('');
	let hostname = $state('');
	let port = $state(6697);
	let tls = $state(true);
	let tags = $state('');
	let languages = $state('en');
	let nsfw = $state(false);
	let website = $state('');
	let busy = $state(false);
	let formError = $state('');
	let checking = $state(false);
	let verify = $state<VerifyResult | null>(null);
	let verifyError = $state('');

	// The panel's own directory settings (registration on/off, the hostname)
	// live in the stack config file when there is one.
	let fileMode = $derived(!!session.info?.features.config_file);
	let canPanel = $derived(fileMode && session.can('directory.manage'));
	let regKey = $derived(stackConfig.key('admin.directory_registration'));
	let hostKey = $derived(stackConfig.key('admin.directory_hostname'));
	let newHost = $state('');
	let panelBusy = $state(false);
	let panelError = $state('');
	$effect(() => {
		if (fileMode && !stackConfig.view) void stackConfig.load();
	});
	async function setPanel(set: Record<string, boolean | string>, done: string) {
		panelBusy = true;
		panelError = '';
		try {
			await stackConfig.save({ set });
			if (session.info && 'admin.directory_registration' in set)
				session.info.features.directory_registration = set['admin.directory_registration'] as boolean;
			toasts.push(done);
			hostname = '';
			await load();
		} catch (e) {
			panelError = errorText(e).replace(/^admin\.directory_hostname: /, '');
		} finally {
			panelBusy = false;
		}
	}

	async function load(host = '') {
		error = '';
		try {
			st = await api<DirectoryStatus>(
				'GET',
				'/directory' + (host ? '?hostname=' + encodeURIComponent(host) : '')
			);
			if (!hostname) hostname = st.hostname;
			if (st.port) port = st.port;
			const n = st.listing?.network;
			if (n) fill(n);
		} catch (e) {
			error = errorText(e);
		}
	}
	// The stack's own directory (directory profile): its runtime settings
	// (directory >= 1.4.0; stored in its database, the env only holds
	// defaults) and its peers.
	let local = $derived(!!session.info?.features.directory_local);
	let fed = $state<Federation | null>(null);
	let fedError = $state('');
	let includeMain = $state(true);
	let switching = $state(false);
	let ds = $derived(fed?.settings ?? null);
	let others = $derived(fed?.peers.filter((p) => !p.builtin) ?? []);

	// identity form
	let dName = $state('');
	let dOperator = $state('');
	let dContact = $state('');
	let dSync = $state('10m');
	let savingSettings = $state(false);
	let settingsError = $state('');
	const syncChoices = ['5m', '10m', '30m', '1h', '6h', '24h'];
	let settingsDirty = $derived(
		!!ds &&
			(dName.trim() !== ds.settings.name ||
				dOperator.trim() !== ds.settings.operator ||
				dContact.trim() !== ds.settings.contact ||
				dSync !== ds.settings.sync_every)
	);

	function applyFed(f: Federation) {
		fed = f;
		includeMain = f.settings ? f.settings.main_active : !!f.main_peer?.enabled;
		if (f.settings) {
			dName = f.settings.settings.name;
			dOperator = f.settings.settings.operator;
			dContact = f.settings.settings.contact;
			dSync = f.settings.settings.sync_every;
		}
	}
	async function loadFederation() {
		fedError = '';
		try {
			applyFed(await api<Federation>('GET', '/directory/federation'));
		} catch (e) {
			fedError = errorText(e);
		}
	}
	async function setIncludeMain(on: boolean) {
		switching = true;
		try {
			applyFed(await api<Federation>('PUT', '/directory/federation/include-main', { enabled: on }));
			toasts.push(
				on
					? 'Your directory includes the boson main directory. Its listings show up after their DNS check.'
					: 'Your directory no longer includes the boson main directory. Its listings are gone.'
			);
		} catch (e) {
			includeMain = !on;
			toasts.push(errorText(e), 'bad');
		} finally {
			switching = false;
		}
	}
	async function saveSettings(e: Event) {
		e.preventDefault();
		if (!ds) return;
		savingSettings = true;
		settingsError = '';
		const body: Record<string, string> = {};
		if (dName.trim() !== ds.settings.name) body.name = dName.trim();
		if (dOperator.trim() !== ds.settings.operator) body.operator = dOperator.trim();
		if (dContact.trim() !== ds.settings.contact) body.contact = dContact.trim();
		if (dSync !== ds.settings.sync_every) body.sync_every = dSync;
		try {
			applyFed(await api<Federation>('PATCH', '/directory/settings', body));
			toasts.push('Directory settings saved. They apply right away.');
		} catch (err) {
			settingsError = errorText(err);
		} finally {
			savingSettings = false;
		}
	}

	// peers
	let peerInput = $state('');
	let peerCheck = $state<PeerCheck | null>(null);
	let peerError = $state('');
	let checkingPeer = $state(false);
	let addingPeer = $state(false);
	let peerBusy = $state<Record<string, boolean>>({});
	async function checkPeer(e: Event) {
		e.preventDefault();
		peerCheck = null;
		peerError = '';
		if (!peerInput.trim()) return;
		checkingPeer = true;
		try {
			peerCheck = await api<PeerCheck>(
				'GET',
				'/directory/peers/check?url=' + encodeURIComponent(peerInput.trim())
			);
		} catch (err) {
			peerError = errorText(err);
		} finally {
			checkingPeer = false;
		}
	}
	async function addPeer() {
		if (!peerCheck) return;
		addingPeer = true;
		try {
			applyFed(await api<Federation>('POST', '/directory/peers', { url: peerCheck.url }));
			toasts.push(
				`Importing from ${peerCheck.well_known.name || peerName(peerCheck.url)}. The first sync starts within a minute.`
			);
			peerCheck = null;
			peerInput = '';
		} catch (err) {
			peerError = errorText(err);
		} finally {
			addingPeer = false;
		}
	}
	async function setPeer(p: DirectoryPeer, on: boolean) {
		peerBusy[p.id] = true;
		try {
			applyFed(
				await api<Federation>('PATCH', `/directory/peers/${encodeURIComponent(p.id)}`, { enabled: on })
			);
			toasts.push(
				on
					? `Importing from ${p.name || peerName(p.url)} again.`
					: `Stopped importing from ${p.name || peerName(p.url)}. Its listings are hidden.`
			);
		} catch (err) {
			toasts.push(errorText(err), 'bad');
			await loadFederation();
		} finally {
			peerBusy[p.id] = false;
		}
	}
	async function syncPeer(p: DirectoryPeer) {
		peerBusy[p.id] = true;
		try {
			const r = await api<PeerSyncResult>('POST', `/directory/peers/${encodeURIComponent(p.id)}/sync`);
			applyFed(r.federation);
			if (r.result.error)
				toasts.push(`Sync with ${p.name || peerName(p.url)} failed: ${r.result.error}`, 'bad');
			else
				toasts.push(
					`Synced with ${p.name || peerName(p.url)}: ${r.result.created} new, ${r.result.updated} updated, ${r.result.deleted} removed.`
				);
		} catch (err) {
			toasts.push(errorText(err), 'bad');
		} finally {
			peerBusy[p.id] = false;
		}
	}
	async function removePeer(p: DirectoryPeer) {
		try {
			applyFed(await api<Federation>('DELETE', `/directory/peers/${encodeURIComponent(p.id)}`));
			toasts.push(`Removed ${p.name || peerName(p.url)}. Its listings disappear from your directory.`);
		} catch (err) {
			toasts.push(errorText(err), 'bad');
			throw err;
		}
	}
	const peerName = (u: string) => {
		try {
			return new URL(u).host;
		} catch {
			return u;
		}
	};

	onMount(() => {
		if (!off) load();
		if (local) loadFederation();
	});

	function fill(n: DirectoryServer) {
		name = n.name;
		description = n.description ?? '';
		tags = (n.tags ?? []).join(', ');
		languages = (n.languages ?? []).join(', ');
		nsfw = n.is_nsfw;
		website = n.website_url ?? '';
	}
	const list = (s: string) =>
		s
			.split(/[,\s]+/)
			.map((x) => x.trim())
			.filter(Boolean);

	let listing = $derived<Listing | undefined>(st?.listing);
	let status = $derived(listing?.network.verification_status ?? (st?.public ? 'verified' : ''));

	async function submit(e: Event) {
		e.preventDefault();
		busy = true;
		formError = '';
		const fields = {
			name: name.trim(),
			description: description.trim(),
			tags: list(tags),
			languages: list(languages),
			is_nsfw: nsfw,
			website_url: website.trim()
		};
		try {
			if (listing) {
				const l = await api<Listing>(
					'PATCH',
					`/directory/${encodeURIComponent(listing.network.id)}?hostname=${encodeURIComponent(listing.network.hostname)}`,
					fields
				);
				st = { ...st!, listing: l };
				toasts.push('Listing updated.');
			} else {
				const l = await api<Listing>('POST', '/directory', {
					...fields,
					hostname: hostname.trim(),
					port,
					tls
				});
				st = { ...st!, listing: l, hostname: l.network.hostname };
				toasts.push('Submitted. Add the DNS record to finish.');
			}
		} catch (err) {
			formError = errorText(err);
		} finally {
			busy = false;
		}
	}

	function q(l: Listing) {
		return `${encodeURIComponent(l.network.id)}?hostname=${encodeURIComponent(l.network.hostname)}`;
	}
	async function check() {
		if (!listing) return;
		checking = true;
		verifyError = '';
		try {
			const id = encodeURIComponent(listing.network.id);
			verify = await api<VerifyResult>(
				'POST',
				`/directory/${id}/verify?hostname=${encodeURIComponent(listing.network.hostname)}`
			);
			if (verify.success) {
				toasts.push('Verified. The network is listed in the boson directory.');
				await load(listing.network.hostname);
			}
		} catch (e) {
			verifyError = errorText(e);
		} finally {
			checking = false;
		}
	}
	async function regenerate() {
		if (!listing) return;
		try {
			const l = await api<Listing>('POST', `/directory/${q(listing).replace('?', '/regenerate-token?')}`);
			st = { ...st!, listing: l };
			verify = null;
			toasts.push('New verification record issued. Update the TXT record.');
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
	}
	async function unlist() {
		if (!listing) return;
		try {
			await api('DELETE', `/directory/${q(listing)}`);
			toasts.push('Removed from the directory.');
			st = { ...st!, listing: undefined, public: undefined };
		} catch (e) {
			toasts.push(errorText(e), 'bad');
			throw e;
		}
	}

	const resolverNames: Record<string, string> = {
		cloudflare: 'Cloudflare 1.1.1.1',
		google: 'Google 8.8.8.8',
		quad9: 'Quad9 9.9.9.9'
	};
	const outcome: Record<string, string> = {
		match: 'Sees the record',
		missing_record: 'No record yet',
		timeout: 'Timed out',
		error: 'Lookup failed'
	};
</script>

<svelte:head><title>Directory · boson admin</title></svelte:head>

<div class="page-head">
	<div>
		<h1>Directory</h1>
		<p>
			List this network in the boson directory{st?.api_url ? ` (${peerName(st.api_url)})` : ''} so people can find
			it from the boson app’s server browser.
		</p>
	</div>
</div>

{#if off}
	<State kind="off" title="The directory is off">
		<code>ADMIN_DIRECTORY_API_URL</code> is empty, so the panel doesn’t talk to the boson directory. It’s a
		deployment setting: set it in the stack’s <code>.env</code> (default
		<code>https://directory.boson.chat</code>) and run
		<code>docker compose up -d admin</code>.
	</State>
{:else if error}
	<State kind="error">
		{error}
		{#snippet action()}<button class="btn btn-small" onclick={() => load()}>Try again</button>{/snippet}
	</State>
{:else if !st}
	<State kind="loading" title="Checking the directory…" />
{:else}
	<!-- where this network stands -->
	<section class="standing" data-s={status || 'none'} aria-labelledby="st-h">
		<span class="dot" aria-hidden="true"></span>
		<div>
			<h2 id="st-h">
				{#if status === 'verified'}Listed in the directory
				{:else if status === 'pending'}Waiting for the DNS record
				{:else if status === 'lapsed'}Listing lapsed
				{:else}Not listed{/if}
			</h2>
			<p>
				{#if status === 'verified'}
					{(listing?.network ?? st.public)?.name} at <code>{st.hostname}</code> shows up in the boson app.
					{#if (listing?.network ?? st.public)?.verification_last_checked_at}
						The TXT record was last checked <Time
							at={(listing?.network ?? st.public)?.verification_last_checked_at}
						/>; keep it in place, the directory re-checks it daily.
					{/if}
				{:else if status === 'pending'}
					Submitted <Time at={listing?.network.registered_at} />. Add the TXT record below, then check it.
				{:else if status === 'lapsed'}
					The TXT record went missing, so the listing is hidden. Issue a new record and verify again.
				{:else if st.hostname}
					<code>{st.hostname}</code> isn’t in the public directory{st.public_error
						? ' (the search failed: ' + st.public_error + ')'
						: ''}.
				{:else if canPanel}
					Which IRC hostname should this page list and check?
				{:else}
					No IRC hostname to check. {fileMode
						? 'Someone with directory.manage can set it here.'
						: 'Set DIRECTORY_HOSTNAME (or IRC_CHECK_SERVER_NAME) on the admin service and recreate it.'}
				{/if}
			</p>
		</div>
	</section>

	{#if !st.hostname && canPanel}
		<form
			class="section host-form"
			onsubmit={(e) => {
				e.preventDefault();
				setPanel(
					{ 'admin.directory_hostname': newHost },
					`The Directory page now lists ${newHost.trim().toLowerCase()}.`
				);
			}}
		>
			<label for="dir-host">Public IRC hostname</label>
			<div class="row">
				<input
					type="text"
					id="dir-host"
					class="mono"
					placeholder="irc.example.org"
					spellcheck="false"
					autocomplete="off"
					bind:value={newHost}
					aria-invalid={!!panelError}
					aria-describedby="dir-host-hint"
				/>
				<button class="btn btn-primary" disabled={!newHost.trim() || panelBusy}
					>{panelBusy ? 'Saving…' : 'Save hostname'}</button
				>
			</div>
			<p class="small muted" id="dir-host-hint">
				{#if panelError}<span class="perr">{panelError}</span>{:else}Saved to the stack config file as
					<code>admin.directory_hostname</code>.{/if}
			</p>
		</form>
	{/if}

	{#if !st.registration || st.unsupported}
		<div class="section">
			{#if st.unsupported}
				<State kind="unsupported" title="This directory can’t take network registrations">
					<p>{st.unsupported}.</p>
					<p>
						Networks register with a network key and a DNS record through the directory’s <code>/v1</code>
						API. Point <code>ADMIN_DIRECTORY_API_URL</code> in the stack’s <code>.env</code> at a boson
						directory (default <code>https://directory.boson.chat</code>) and run
						<code>docker compose up -d admin</code>.
					</p>
				</State>
			{:else}
				<State kind="off" title="Registration is switched off">
					<p>
						This page only checks whether the network is listed.
						{#if canPanel}
							Turn registration on and it walks you through listing it:
						{:else if fileMode}
							Someone with <code>directory.manage</code> can turn it on here, and the page then walks you through
							it:
						{:else}
							To turn it on, set <code>ADMIN_DIRECTORY_REGISTRATION=true</code> in the stack’s
							<code>.env</code>
							and run
							<code>docker compose up -d admin</code>. The page then walks you through it:
						{/if}
					</p>
					<ol class="how">
						<li>Fill in the network’s name, description and IRC address.</li>
						<li>
							Add a TXT record that proves you run the hostname:
							<code class="rec">_boson.{st.hostname || 'irc.example.org'}. 300 IN TXT "boson-verify=…"</code>
						</li>
						<li>
							Check it from here. The directory re-checks it daily and hides the listing if it disappears.
						</li>
					</ol>
					{#if canPanel}
						<p class="turn-on">
							<button
								class="btn btn-primary btn-small"
								disabled={panelBusy}
								onclick={() =>
									setPanel({ 'admin.directory_registration': true }, 'Directory registration is on.')}
								>{panelBusy ? 'Turning on…' : 'Turn on registration'}</button
							>
						</p>
						{#if panelError}<p class="perr small" role="alert">{panelError}</p>{/if}
					{/if}
				</State>
			{/if}
		</div>
	{:else}
		{#if listing && listing.verification && status !== 'verified'}
			<section class="section" aria-labelledby="dns-h">
				<div class="section-head">
					<h2 id="dns-h">Prove you run {listing.network.hostname}</h2>
					{#if listing.verification.expires_at}
						<p>Record valid until {datetime(listing.verification.expires_at)}</p>
					{/if}
				</div>
				<ol class="steps">
					<li class="done">
						<h3>Submit the listing</h3>
						<p>Done <Time at={listing.network.registered_at} />.</p>
					</li>
					<li>
						<h3>Add this TXT record at your DNS provider</h3>
						<div class="rec-grid">
							<CopyField label="Name" value={listing.verification.record_name} />
							<CopyField label="Value" value={listing.verification.record_value} />
						</div>
						<p class="muted small">
							Some DNS providers want only <code
								>_boson.{listing.network.hostname.split('.').slice(0, -2).join('.')}</code
							>
							as the name, without the domain. A TTL of 300 seconds keeps the wait short.
						</p>
					</li>
					<li>
						<h3>Check it</h3>
						<p>The directory asks three public resolvers. All three must see the record.</p>
						<div class="check">
							<button class="btn btn-primary btn-small" onclick={check} disabled={checking}
								>{checking ? 'Checking…' : 'Check DNS now'}</button
							>
							<Confirm title="Issue a new record?" action="Issue new record" onconfirm={regenerate}>
								{#snippet trigger(open)}<button class="btn btn-small btn-ghost" onclick={open}
										>New record</button
									>{/snippet}
								The current value stops working. Use this if it expired or leaked.
							</Confirm>
						</div>
						{#if verifyError}<p class="verr" role="alert">{verifyError}</p>{/if}
						{#if verify}
							<ul class="resolvers" aria-live="polite">
								{#each Object.entries(verify.report.results) as [k, r] (k)}
									<li data-o={r.outcome}>
										<span class="rname">{resolverNames[k] ?? k}</span>
										<span class="rout">{outcome[r.outcome] ?? r.outcome}</span>
										{#if r.detail}<span class="muted rdet">{r.detail}</span>{/if}
									</li>
								{/each}
							</ul>
							{#if !verify.success}
								<p class="muted small">
									DNS changes can take a few minutes to reach every resolver. The directory also checks
									pending listings daily on its own.
								</p>
							{/if}
						{/if}
					</li>
				</ol>
			</section>
		{/if}

		<section class="section" aria-labelledby="form-h">
			<div class="section-head">
				<h2 id="form-h">{listing ? 'Listing details' : 'Submit this network'}</h2>
				{#if !listing}<p>Shown to everyone browsing the directory.</p>{/if}
			</div>
			<form class="dform" onsubmit={submit}>
				<div class="row2">
					<label class="field">
						<span>Network name</span>
						<input type="text" bind:value={name} required maxlength="64" />
					</label>
					<label class="field">
						<span>Website</span>
						<input
							type="text"
							inputmode="url"
							bind:value={website}
							placeholder="https://"
							spellcheck="false"
						/>
					</label>
				</div>
				<label class="field">
					<span>Description</span>
					<textarea bind:value={description} rows="3" maxlength="500"></textarea>
					<small>{description.length}/500. What people talk about here and who it’s for.</small>
				</label>
				<div class="row3">
					<label class="field">
						<span>IRC hostname</span>
						<input type="text" bind:value={hostname} required readonly={!!listing} spellcheck="false" />
						<small>{listing ? 'Fixed once submitted.' : 'The public name clients connect to.'}</small>
					</label>
					<label class="field">
						<span>Port</span>
						<input type="number" bind:value={port} min="1" max="65535" required readonly={!!listing} />
					</label>
					<div class="field tlsf">
						<Toggle label="TLS" bind:checked={tls} disabled={!!listing} />
					</div>
				</div>
				<div class="row2">
					<label class="field">
						<span>Tags</span>
						<input type="text" bind:value={tags} placeholder="open-source, gaming" spellcheck="false" />
						<small>Up to 8, comma-separated.</small>
					</label>
					<label class="field">
						<span>Languages</span>
						<input type="text" bind:value={languages} placeholder="en, pt-BR" spellcheck="false" />
					</label>
				</div>
				<Toggle
					label="Adult content"
					detail="Hidden from people who haven’t opted in to NSFW servers."
					bind:checked={nsfw}
				/>
				{#if formError}<p class="verr" role="alert">{formError}</p>{/if}
				<div class="submit">
					<button class="btn btn-primary" disabled={busy}
						>{busy ? 'Saving…' : listing ? 'Save listing' : 'Submit for listing'}</button
					>
				</div>
			</form>
		</section>

		{#if listing}
			<section class="section" aria-labelledby="rm-h">
				<div class="section-head"><h2 id="rm-h">Remove from the directory</h2></div>
				<div class="rm">
					<p>
						The network disappears from the server browser. People who already joined keep their connection
						details.
					</p>
					<Confirm
						title="Remove {listing.network.name} from the directory?"
						action="Remove listing"
						danger
						typed={listing.network.hostname}
						onconfirm={unlist}
					>
						{#snippet trigger(open)}<button class="btn btn-small btn-danger" onclick={open}
								>Remove listing</button
							>{/snippet}
						You can submit it again later; it needs a fresh DNS check.
					</Confirm>
				</div>
			</section>
		{/if}
	{/if}
{/if}

{#snippet peerStats(p: DirectoryPeer)}
	<span>{p.listing_count} listings</span>
	<span
		>Last sync {#if p.last_sync_at}<Time at={p.last_sync_at} />{:else}not yet{/if}</span
	>
{/snippet}

{#if local}
	<section class="section" aria-labelledby="fed-h">
		<div class="section-head">
			<h2 id="fed-h">Your own directory</h2>
			<p>
				This stack runs a directory of its own{fed ? ' at ' : ''}{#if fed}<code
						>{ds?.public_url || fed.url}</code
					>{/if}. Changes here apply right away and stick across restarts; no <code>.env</code> edits.
			</p>
		</div>
		{#if fedError}
			<State kind="error" title="The stack’s directory didn’t answer">
				{fedError}
				{#snippet action()}<button class="btn btn-small" onclick={loadFederation}>Try again</button>{/snippet}
			</State>
		{:else if !fed}
			<State kind="loading" title="Asking the stack’s directory…" />
		{:else if fed.unsupported}
			<State kind="unsupported" title="Needs a newer directory">{fed.unsupported}.</State>
		{:else if fed.error}
			<State kind="error" title="The stack’s directory didn’t answer">
				{fed.error}
				{#snippet action()}<button class="btn btn-small" onclick={loadFederation}>Try again</button>{/snippet}
			</State>
		{:else}
			<div class="fed">
				{#if fed.settings_unsupported}
					<Notice tone="info">
						Your directory is older than 1.4.0, so its name, contact and main-directory setting can’t be
						changed from here yet. Update the directory service to manage them in the panel. Peers work
						already.
					</Notice>
				{:else if fed.settings_error}
					<Notice tone="warn">Couldn’t read the directory’s settings: {fed.settings_error}</Notice>
				{/if}

				<!-- the main directory -->
				<div class="card" aria-labelledby="main-h">
					<h3 id="main-h" class="sub">The boson main directory</h3>
					{#if ds?.is_main}
						<div class="ismain">
							<span class="tag tag-accent">Main directory</span>
							<p>
								This is the boson main directory: other directories include it. It doesn’t import itself, so
								there’s nothing to switch on here.
							</p>
						</div>
					{:else}
						<Toggle
							label="Include the boson main directory"
							detail={ds || fed.main_peer
								? `Also show every network listed at ${peerName(ds?.settings.main_peer_url || fed.main_peer?.url || 'https://directory.boson.chat')}. Each one appears here only after your directory has checked its DNS record itself.`
								: 'Off. Switching it on from here needs directory 1.4.0 or newer.'}
							bind:checked={includeMain}
							disabled={(!ds && !fed.main_peer) || switching}
							onchange={setIncludeMain}
						/>
						{#if includeMain && fed.main_peer}
							<div class="pstats muted small">
								{@render peerStats(fed.main_peer)}
								{#if fed.main_peer.last_error}<span class="perr">{fed.main_peer.last_error}</span>{/if}
							</div>
						{/if}
					{/if}
				</div>

				<!-- identity -->
				{#if ds}
					<form class="card dsettings" aria-labelledby="id-h" onsubmit={saveSettings}>
						<h3 id="id-h" class="sub">Directory details</h3>
						<p class="muted small">
							Shown to people browsing your directory and to other directories that import it.
						</p>
						<div class="row2">
							<label class="field">
								<span>Name</span>
								<input type="text" bind:value={dName} required maxlength="100" />
							</label>
							<label class="field">
								<span>Operator</span>
								<input type="text" bind:value={dOperator} maxlength="100" placeholder="Who runs it" />
							</label>
						</div>
						<div class="row2">
							<label class="field">
								<span>Contact</span>
								<input
									type="text"
									bind:value={dContact}
									maxlength="200"
									placeholder="Email or URL"
									spellcheck="false"
								/>
							</label>
							<label class="field">
								<span>Sync peers every</span>
								<select bind:value={dSync}>
									{#each syncChoices.includes(ds.settings.sync_every) ? syncChoices : [ds.settings.sync_every, ...syncChoices] as c (c)}
										<option value={c}>{c.replace(/m$/, ' min').replace(/h$/, ' h')}</option>
									{/each}
								</select>
							</label>
						</div>
						{#if settingsError}<p class="verr" role="alert">{settingsError}</p>{/if}
						<div class="submit">
							{#if ds.updated_by}<span class="muted small"
									>Last changed by {ds.updated_by}{#if ds.updated_at}, <Time at={ds.updated_at} />{/if}</span
								>{/if}
							<button class="btn btn-primary btn-small" disabled={!settingsDirty || savingSettings}
								>{savingSettings ? 'Saving…' : 'Save details'}</button
							>
						</div>
					</form>
				{/if}

				<!-- peers -->
				<div class="card" aria-labelledby="peers-h">
					<h3 id="peers-h" class="sub">Other directories</h3>
					<p class="muted small">
						Import listings from any directory that publishes <code>/.well-known/boson-directory.json</code>.
					</p>
					<form class="addpeer" onsubmit={checkPeer}>
						<label class="field">
							<span class="sr-only">Directory address</span>
							<input
								type="text"
								inputmode="url"
								bind:value={peerInput}
								placeholder="https://directory.example.org"
								spellcheck="false"
								oninput={() => {
									peerCheck = null;
									peerError = '';
								}}
							/>
						</label>
						<button class="btn btn-small" disabled={checkingPeer || !peerInput.trim()}
							>{checkingPeer ? 'Checking…' : 'Check'}</button
						>
					</form>
					{#if peerError}<p class="verr" role="alert">{peerError}</p>{/if}
					{#if peerCheck}
						<div class="found" aria-live="polite">
							<div>
								<span class="pname">{peerCheck.well_known.name || peerName(peerCheck.url)}</span>
								<span class="mono muted small">{peerCheck.url}</span>
								{#if peerCheck.well_known.operator}<span class="muted small"
										>run by {peerCheck.well_known.operator}</span
									>{/if}
							</div>
							<button class="btn btn-primary btn-small" onclick={addPeer} disabled={addingPeer}
								>{addingPeer ? 'Adding…' : 'Add peer'}</button
							>
						</div>
					{/if}
					{#if others.length === 0}
						<p class="muted small">No other directories yet.</p>
					{:else}
						<ul class="peers">
							{#each others as p (p.id)}
								<li data-on={p.enabled}>
									<div class="pwhat">
										<div class="pline">
											<span class="pname">{p.name || peerName(p.url)}</span>
											{#if !p.enabled}<span class="tag">off</span>{/if}
										</div>
										<span class="mono muted small">{p.url}</span>
										<div class="pstats muted small">{@render peerStats(p)}</div>
										{#if p.last_error}<p class="perr small">{p.last_error}</p>{/if}
									</div>
									<div class="pctl">
										<Toggle
											label={p.enabled ? 'Importing' : 'Not importing'}
											checked={p.enabled}
											disabled={peerBusy[p.id]}
											onchange={(v) => setPeer(p, v)}
										/>
										<div class="pbtns">
											<button
												class="btn btn-small"
												onclick={() => syncPeer(p)}
												disabled={!p.enabled || peerBusy[p.id]}
												>{peerBusy[p.id] ? 'Working…' : 'Sync now'}</button
											>
											<Confirm
												title="Remove {p.name || peerName(p.url)}?"
												action="Remove peer"
												danger
												onconfirm={() => removePeer(p)}
											>
												{#snippet trigger(open)}<button class="btn btn-small btn-ghost" onclick={open}
														>Remove</button
													>{/snippet}
												Its {p.listing_count} imported listings disappear from your directory. Networks registered
												directly with it aren’t affected.
											</Confirm>
										</div>
									</div>
								</li>
							{/each}
						</ul>
					{/if}
				</div>
			</div>
		{/if}
	</section>
{/if}

{#if st && fileMode && (regKey || hostKey)}
	<p class="panel-foot small muted">
		This page lists <code>{st.hostname || 'no hostname'}</code>{hostKey?.set
			? ' (set in the stack config file)'
			: hostKey?.default
				? ' (the default from the environment)'
				: ''}; registration is {st.registration ? 'on' : 'off'}. Change either on
		<a href="/config#g-admin">Stack config</a>.
	</p>
{/if}

<style>
	.host-form {
		display: grid;
		gap: var(--space-2);
		max-width: 34rem;
	}
	.host-form label {
		font-weight: 600;
		font-size: var(--text-sm);
	}
	.host-form .row {
		display: flex;
		gap: var(--space-2);
	}
	.host-form input {
		flex: 1;
	}
	.turn-on {
		margin-top: var(--space-4);
	}
	.panel-foot {
		margin-top: var(--space-8);
		max-width: 64ch;
	}
	.standing {
		display: flex;
		gap: var(--space-4);
		align-items: flex-start;
		padding: var(--space-5);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
		max-width: 820px;
	}
	.standing h2 {
		font-size: var(--text-lg);
	}
	.standing p {
		margin-top: var(--space-1);
		color: var(--fg-2);
		font-size: var(--text-sm);
	}
	.standing .dot {
		width: 12px;
		height: 12px;
		margin-top: 5px;
		box-shadow: inset 0 0 0 2px var(--fg-3);
		background: transparent;
	}
	[data-s='verified'] .dot {
		background: var(--success);
		box-shadow: 0 0 0 4px color-mix(in oklab, var(--success), transparent 80%);
	}
	[data-s='pending'] .dot {
		background: var(--warn);
		box-shadow: 0 0 0 4px color-mix(in oklab, var(--warn), transparent 80%);
	}
	[data-s='lapsed'] .dot {
		background: var(--danger);
	}
	[data-s='verified'] {
		border-left: 3px solid var(--success);
	}
	[data-s='pending'] {
		border-left: 3px solid var(--warn);
	}
	[data-s='lapsed'] {
		border-left: 3px solid var(--danger);
	}
	.how {
		margin: var(--space-3) 0 0;
		padding-left: var(--space-5);
		display: grid;
		gap: var(--space-2);
	}
	.rec {
		display: block;
		margin-top: var(--space-2);
		padding: var(--space-2) var(--space-3);
		overflow-wrap: anywhere;
	}
	/* the three steps really are a sequence */
	.steps {
		list-style: none;
		counter-reset: s;
		margin: 0;
		padding: 0;
		display: grid;
		max-width: 820px;
		border-left: 2px solid var(--border);
		margin-left: 11px;
	}
	.steps > li {
		counter-increment: s;
		position: relative;
		padding: 0 0 var(--space-8) var(--space-8);
		display: grid;
		gap: var(--space-3);
	}
	.steps > li:last-child {
		padding-bottom: 0;
	}
	.steps > li::before {
		content: counter(s);
		position: absolute;
		left: -13px;
		top: -2px;
		width: 24px;
		height: 24px;
		display: grid;
		place-items: center;
		border-radius: 50%;
		background: var(--bg);
		border: 2px solid var(--accent);
		color: var(--accent-text);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 700;
	}
	.steps > li.done::before {
		content: '✓';
		background: var(--accent);
		color: var(--accent-on);
	}
	.steps h3 {
		font-size: var(--text-base);
	}
	.steps p {
		color: var(--fg-2);
		font-size: var(--text-sm);
	}
	.rec-grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-3);
	}
	.small {
		font-size: var(--text-xs);
	}
	.check {
		display: flex;
		gap: var(--space-2);
		flex-wrap: wrap;
	}
	.resolvers {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.resolvers li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-4);
		align-items: center;
		padding: var(--space-2) var(--space-4);
		border-bottom: 1px solid var(--border-soft);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
	}
	.resolvers li:last-child {
		border-bottom: 0;
	}
	.rname {
		min-width: 18ch;
	}
	.rout {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		color: var(--danger-text);
	}
	.rout::before {
		content: '';
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--danger);
	}
	[data-o='match'] .rout {
		color: var(--success-text);
	}
	[data-o='match'] .rout::before {
		background: var(--success);
	}
	.rdet {
		font-size: var(--text-xs);
	}
	.verr {
		color: var(--danger-text);
		font-size: var(--text-sm);
	}
	.dform {
		display: grid;
		gap: var(--space-5);
		max-width: 820px;
	}
	.row2 {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
		gap: var(--space-4);
	}
	.row3 {
		display: grid;
		grid-template-columns: minmax(0, 2fr) 120px auto;
		gap: var(--space-4);
		align-items: start;
	}
	.tlsf {
		padding-top: 26px;
	}
	textarea {
		width: 100%;
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-sm);
		border: 1px solid var(--border);
		background: var(--surface);
		color: var(--fg);
		font-family: var(--font-body);
		font-size: var(--text-base);
		resize: vertical;
	}
	textarea:focus-visible {
		outline: none;
		border-color: var(--accent);
		box-shadow:
			0 0 0 1px var(--accent),
			0 0 0 4px var(--accent-dim);
	}
	input[readonly] {
		color: var(--fg-quiet);
		background: var(--bg);
	}
	.rm {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		align-items: center;
		justify-content: space-between;
		padding: var(--space-4);
		border: 1px solid color-mix(in oklab, var(--danger), transparent 70%);
		border-radius: var(--radius-sm);
		max-width: 820px;
	}
	.rm p {
		color: var(--fg-2);
		font-size: var(--text-sm);
		max-width: 60ch;
	}
	.fed {
		display: grid;
		gap: var(--space-4);
		max-width: 820px;
	}
	.card {
		display: grid;
		gap: var(--space-3);
		padding: var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.sub {
		font-size: var(--text-base);
	}
	.ismain {
		display: grid;
		gap: var(--space-2);
		justify-items: start;
	}
	.ismain p {
		color: var(--fg-2);
		font-size: var(--text-sm);
		max-width: 62ch;
	}
	.dsettings .submit {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: center;
		justify-content: flex-end;
	}
	select {
		width: 100%;
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-sm);
		border: 1px solid var(--border);
		background: var(--surface);
		color: var(--fg);
		font-size: var(--text-base);
	}
	.addpeer {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: var(--space-2);
		align-items: center;
	}
	.found {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: center;
		justify-content: space-between;
		padding: var(--space-3);
		border: 1px solid color-mix(in oklab, var(--accent), transparent 60%);
		border-radius: var(--radius-sm);
		background: var(--accent-dim);
	}
	.found > div {
		display: grid;
		gap: 2px;
		min-width: 0;
	}
	.peers {
		list-style: none;
		margin: 0;
		padding: 0;
		border: 1px solid var(--border-soft);
		border-radius: var(--radius-sm);
	}
	.peers li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3) var(--space-5);
		align-items: flex-start;
		justify-content: space-between;
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}
	.peers li:last-child {
		border-bottom: 0;
	}
	.pwhat {
		display: grid;
		gap: 2px;
		min-width: 0;
		flex: 1 1 260px;
	}
	.pline {
		display: flex;
		gap: var(--space-2);
		align-items: center;
	}
	.pstats {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-4);
	}
	.pctl {
		display: grid;
		gap: var(--space-2);
		justify-items: end;
	}
	.pbtns {
		display: flex;
		gap: var(--space-2);
	}
	.peers li[data-on='false'] .pname {
		color: var(--fg-quiet);
	}
	.pname {
		font-family: var(--font-mono);
		font-weight: 600;
	}
	.mono {
		font-family: var(--font-mono);
		overflow-wrap: anywhere;
	}
	.perr {
		color: var(--danger-text);
		font-family: var(--font-mono);
		overflow-wrap: anywhere;
	}
	@media (max-width: 640px) {
		.row3 {
			grid-template-columns: minmax(0, 1fr);
		}
		.tlsf {
			padding-top: 0;
		}
		.steps > li {
			padding-left: var(--space-5);
		}
		.pctl {
			justify-items: start;
		}
	}
</style>
