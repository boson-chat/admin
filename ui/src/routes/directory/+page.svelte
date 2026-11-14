<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { datetime } from '$lib/format';
	import type { DirectoryServer, DirectoryStatus, Listing, VerifyResult } from '$lib/types';
	import Confirm from '$lib/components/Confirm.svelte';
	import CopyField from '$lib/components/CopyField.svelte';
	import State from '$lib/components/State.svelte';
	import Time from '$lib/components/Time.svelte';
	import Toggle from '$lib/components/Toggle.svelte';

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
	onMount(() => {
		if (!off) load();
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
			List this network in the boson.chat directory so people can find it from the boson app’s server browser.
		</p>
	</div>
</div>

{#if off}
	<State kind="off" title="The directory is off">
		<code>DIRECTORY_API_URL</code> is empty, so the panel doesn’t talk to the boson directory.
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
				{:else}
					Set the IRC hostname to check: <code>DIRECTORY_HOSTNAME</code> or
					<code>IRC_CHECK_SERVER_NAME</code>.
				{/if}
			</p>
		</div>
	</section>

	{#if !st.registration || st.unsupported}
		<div class="section">
			{#if st.unsupported}
				<State kind="unsupported" title="api.boson.chat can’t take self-hosted listings yet">
					<p>{st.unsupported}.</p>
					<p>
						The public directory only accepts listings from signed-in boson.chat accounts today. Self-hosted
						networks will register with a DNS record instead, and this page will do it for you.
					</p>
				</State>
			{:else}
				<State kind="off" title="Self-hosted registration isn’t switched on">
					<p>
						Listing a self-hosted network needs a registration API on api.boson.chat that is still being
						built. Once it’s live, set <code>DIRECTORY_REGISTRATION=true</code> and this page walks you through
						it:
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

<style>
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
	}
</style>
