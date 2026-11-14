<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import { presets, slugify, type Preset } from '$lib/sso';
	import type { SSOProvider } from '$lib/types';
	import Toggle from './Toggle.svelte';
	import SetupGuide from './SetupGuide.svelte';

	/** Adds a provider (no `provider`) or edits one. */
	let { provider, onsaved }: { provider?: SSOProvider; onsaved?: (p: SSOProvider) => void } = $props();
	const editing = $derived(!!provider);
	const uid = $props.id();

	// svelte-ignore state_referenced_locally
	const p0 = provider;
	let presetId = $state(presets[0].id);
	let preset = $derived<Preset>(presets.find((p) => p.id === presetId) ?? presets[0]);
	let kind = $derived(p0?.kind ?? preset.kind);

	let displayName = $state(p0?.display_name ?? presets[0].displayName);
	let slug = $state(p0?.slug ?? presets[0].slug);
	let slugTouched = $state(false);
	let allowSignup = $state(p0?.allow_signup ?? true);
	let trustEmail = $state(p0?.trust_email ?? false);

	const o = p0?.config.oidc;
	let issuer = $state(o?.issuer ?? presets[0].issuer ?? '');
	let clientId = $state(o?.client_id ?? '');
	let clientSecret = $state('');
	let scopes = $state(o?.scopes?.join(' ') ?? presets[0].scopes ?? 'openid email profile');
	let usernameClaim = $state(o?.username_claim ?? '');
	let emailClaim = $state(o?.email_claim ?? '');
	let nameClaim = $state(o?.display_name_claim ?? '');
	const hadSecret = p0?.has_client_secret ?? false;

	const sm = p0?.config.saml;
	let metaMode = $state<'url' | 'xml'>(sm?.idp_metadata_xml ? 'xml' : 'url');
	let metaURL = $state(sm?.idp_metadata_url ?? '');
	let metaXML = $state(sm?.idp_metadata_xml ?? '');
	let entityID = $state(sm?.entity_id ?? '');
	let attrEmail = $state(sm?.attribute_map.email ?? '');
	let attrUser = $state(sm?.attribute_map.username ?? '');
	let attrName = $state(sm?.attribute_map.display_name ?? '');
	const nameIdFormats: Record<string, string> = {
		'urn:oasis:names:tc:SAML:2.0:nameid-format:persistent': 'persistent',
		'urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress': 'email',
		'urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified': 'unspecified'
	};
	let nameIdFormat = $state(nameIdFormats[sm?.name_id_format ?? ''] ?? sm?.name_id_format ?? '');
	let signRequests = $state(sm?.sign_requests ?? false);

	let busy = $state(false);
	let formError = $state('');

	function choose(p: Preset) {
		presetId = p.id;
		displayName = p.displayName;
		if (!slugTouched) slug = p.slug;
		issuer = p.issuer ?? '';
		scopes = p.scopes ?? 'openid email profile';
		metaURL = '';
		attrEmail = p.attributes?.email ?? '';
		attrUser = p.attributes?.username ?? '';
		attrName = p.attributes?.display_name ?? '';
		// Google and the big IdPs verify email addresses; a generic one may not.
		trustEmail = ['google', 'entra', 'okta'].includes(p.id);
	}

	function body() {
		const config =
			kind === 'oidc'
				? {
						oidc: {
							issuer: issuer.trim(),
							client_id: clientId.trim(),
							...(clientSecret ? { client_secret: clientSecret } : {}),
							scopes: scopes.split(/[\s,]+/).filter(Boolean),
							...(usernameClaim.trim() ? { username_claim: usernameClaim.trim() } : {}),
							...(emailClaim.trim() ? { email_claim: emailClaim.trim() } : {}),
							...(nameClaim.trim() ? { display_name_claim: nameClaim.trim() } : {})
						}
					}
				: {
						saml: {
							...(metaMode === 'url'
								? { idp_metadata_url: metaURL.trim() }
								: { idp_metadata_xml: metaXML.trim() }),
							...(entityID.trim() ? { entity_id: entityID.trim() } : {}),
							...(nameIdFormat.trim() ? { name_id_format: nameIdFormat.trim() } : {}),
							sign_requests: signRequests,
							attribute_map: {
								email: attrEmail.trim() || undefined,
								username: attrUser.trim() || undefined,
								display_name: attrName.trim() || undefined
							}
						}
					};
		return {
			display_name: displayName.trim(),
			allow_signup: allowSignup,
			trust_email: trustEmail,
			config,
			...(editing ? {} : { slug: slug.trim(), kind, enabled: false })
		};
	}

	async function submit(e: Event) {
		e.preventDefault();
		busy = true;
		formError = '';
		try {
			if (provider) {
				const r = await api<{ provider: SSOProvider }>(
					'PATCH',
					`/sso/providers/${encodeURIComponent(provider.id)}`,
					body()
				);
				clientSecret = '';
				toasts.push(`Saved ${r.provider.display_name}.`);
				onsaved?.(r.provider);
			} else {
				const r = await api<{ provider: SSOProvider }>('POST', '/sso/providers', body());
				toasts.push(`Added ${r.provider.display_name}. It stays off until you switch it on.`);
				goto(`/sso/${encodeURIComponent(r.provider.id)}`);
			}
		} catch (err) {
			formError = errorText(err);
		} finally {
			busy = false;
		}
	}
</script>

<form class="pform" onsubmit={submit}>
	{#if !editing}
		<fieldset class="presets">
			<legend>Identity provider</legend>
			<div class="grid">
				{#each presets as p (p.id)}
					<label class="preset" class:on={presetId === p.id}>
						<input
							type="radio"
							name="preset-{uid}"
							value={p.id}
							checked={presetId === p.id}
							onchange={() => choose(p)}
						/>
						<span class="pl">{p.label}</span>
						<span class="pk">{p.kind === 'oidc' ? 'OIDC' : 'SAML'}</span>
						<span class="pb">{p.blurb}</span>
					</label>
				{/each}
			</div>
		</fieldset>

		<section class="block" aria-labelledby="guide-{uid}">
			<h2 id="guide-{uid}">Set up {preset.label}</h2>
			<SetupGuide {preset} />
		</section>
	{/if}

	<section class="block" aria-labelledby="basics-{uid}">
		<h2 id="basics-{uid}">Sign-in button</h2>
		<div class="row2">
			<label class="field">
				<span>Name</span>
				<input
					type="text"
					bind:value={displayName}
					required
					maxlength="64"
					oninput={() => {
						if (!editing && !slugTouched) slug = slugify(displayName);
					}}
				/>
				<small>Shown on the button: “Sign in with {displayName || '…'}”.</small>
			</label>
			<label class="field">
				<span>Slug</span>
				<input
					type="text"
					bind:value={slug}
					required
					readonly={editing}
					pattern={'[a-z0-9][a-z0-9\\-]{0,31}'}
					maxlength="32"
					spellcheck="false"
					oninput={() => (slugTouched = true)}
				/>
				<small
					>{editing
						? 'Part of the callback URL, so it can’t change.'
						: 'Part of the callback URL: up to 32 lowercase letters, digits, dashes.'}</small
				>
			</label>
		</div>
	</section>

	{#if kind === 'oidc'}
		<section class="block" aria-labelledby="oidc-{uid}">
			<h2 id="oidc-{uid}">OpenID Connect</h2>
			<label class="field">
				<span>Issuer URL</span>
				<input
					type="text"
					inputmode="url"
					bind:value={issuer}
					required
					placeholder={preset.placeholder}
					spellcheck="false"
				/>
				<small>boson reads &lt;issuer&gt;/.well-known/openid-configuration.</small>
			</label>
			<div class="row2">
				<label class="field">
					<span>Client ID</span>
					<input type="text" bind:value={clientId} required spellcheck="false" autocomplete="off" />
				</label>
				<label class="field">
					<span>Client secret</span>
					<input
						type="password"
						bind:value={clientSecret}
						required={!editing || !hadSecret}
						autocomplete="new-password"
						placeholder={hadSecret ? 'Saved. Leave empty to keep it' : ''}
					/>
					<small>Sent to the auth service only; never shown again.</small>
				</label>
			</div>
			<details class="adv">
				<summary>Scopes and claims</summary>
				<div class="adv-body">
					<label class="field">
						<span>Scopes</span>
						<input type="text" bind:value={scopes} spellcheck="false" />
						<small>Space-separated. openid is always included.</small>
					</label>
					<div class="row2">
						<label class="field">
							<span>Username claim</span>
							<input
								type="text"
								bind:value={usernameClaim}
								placeholder="preferred_username"
								spellcheck="false"
							/>
						</label>
						<label class="field">
							<span>Email claim</span>
							<input type="text" bind:value={emailClaim} placeholder="email" spellcheck="false" />
						</label>
						<label class="field">
							<span>Display name claim</span>
							<input type="text" bind:value={nameClaim} placeholder="name" spellcheck="false" />
						</label>
					</div>
				</div>
			</details>
		</section>
	{:else}
		<section class="block" aria-labelledby="saml-{uid}">
			<h2 id="saml-{uid}">SAML identity provider</h2>
			<div class="seg" role="radiogroup" aria-label="How to give the IdP metadata">
				<label class:on={metaMode === 'url'}
					><input type="radio" bind:group={metaMode} value="url" /> Metadata URL</label
				>
				<label class:on={metaMode === 'xml'}
					><input type="radio" bind:group={metaMode} value="xml" /> Paste XML</label
				>
			</div>
			{#if metaMode === 'url'}
				<label class="field">
					<span>IdP metadata URL</span>
					<input
						type="text"
						inputmode="url"
						bind:value={metaURL}
						required
						placeholder={preset.placeholder}
						spellcheck="false"
					/>
					<small>Fetched by the auth service, so certificate rotations are picked up.</small>
				</label>
			{:else}
				<label class="field">
					<span>IdP metadata XML</span>
					<textarea
						bind:value={metaXML}
						required
						rows="6"
						spellcheck="false"
						placeholder="<md:EntityDescriptor …>"></textarea>
					<small>Paste it again whenever the IdP’s signing certificate changes.</small>
				</label>
			{/if}
			<details class="adv" open={!!(attrEmail || attrUser || attrName)}>
				<summary>Attributes</summary>
				<div class="adv-body">
					<div class="row3">
						<label class="field"
							><span>Email</span><input
								type="text"
								bind:value={attrEmail}
								placeholder="email"
								spellcheck="false"
							/></label
						>
						<label class="field"
							><span>Username</span><input
								type="text"
								bind:value={attrUser}
								placeholder="username"
								spellcheck="false"
							/></label
						>
						<label class="field"
							><span>Display name</span><input
								type="text"
								bind:value={attrName}
								placeholder="displayName"
								spellcheck="false"
							/></label
						>
					</div>
					<div class="row2">
						<label class="field">
							<span>NameID format</span>
							<input
								type="text"
								bind:value={nameIdFormat}
								list="nameid-{uid}"
								placeholder="The IdP’s default"
								spellcheck="false"
							/>
							<datalist id="nameid-{uid}">
								<option value="persistent"></option>
								<option value="email"></option>
								<option value="unspecified"></option>
							</datalist>
							<small>persistent, email, unspecified or a full URN.</small>
						</label>
						<div class="field sign">
							<Toggle
								label="Sign requests"
								detail="Sign AuthnRequests with boson’s SP key; give the IdP the SP certificate."
								bind:checked={signRequests}
							/>
						</div>
					</div>
					<label class="field">
						<span>IdP entity ID</span>
						<input
							type="text"
							bind:value={entityID}
							spellcheck="false"
							placeholder="Only if the metadata lists several"
						/>
					</label>
				</div>
			</details>
		</section>
	{/if}

	<section class="block toggles" aria-label="Accounts">
		<Toggle
			label="Create accounts on first sign-in"
			detail="Someone without a boson account gets one the first time they sign in here. Off: only people who already have an account (linked by an admin or by email) can use it. Closed registration blocks new accounts either way."
			bind:checked={allowSignup}
		/>
		<Toggle
			label="Trust the provider’s email addresses"
			detail="Treat the email address it sends as verified. Only for providers that verify emails themselves: where people can type any address, they could claim someone else’s."
			bind:checked={trustEmail}
		/>
	</section>

	{#if formError}<p class="ferr" role="alert">{formError}</p>{/if}
	<div class="submit">
		<button class="btn btn-primary" disabled={busy}
			>{busy ? 'Saving…' : editing ? 'Save changes' : 'Add provider'}</button
		>
		{#if !editing}<span class="muted note">It’s added switched off. Test it, then switch it on.</span>{/if}
	</div>
</form>

<style>
	.pform {
		display: grid;
		gap: var(--space-8);
		max-width: 820px;
	}
	.block {
		display: grid;
		gap: var(--space-4);
	}
	h2 {
		font-size: var(--text-base);
	}
	.presets {
		border: 0;
		margin: 0;
		padding: 0;
		min-width: 0;
	}
	legend {
		padding: 0;
		font-family: var(--font-display);
		font-weight: 700;
		font-size: var(--text-base);
		margin-bottom: var(--space-3);
	}
	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
		gap: var(--space-2);
	}
	.preset {
		position: relative;
		display: grid;
		grid-template-columns: 1fr auto;
		gap: 2px var(--space-2);
		padding: var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
		cursor: pointer;
		transition:
			border-color var(--motion-fast) var(--ease-standard),
			background-color var(--motion-fast) var(--ease-standard);
	}
	.preset:hover {
		border-color: var(--border-strong);
	}
	.preset.on {
		border-color: var(--accent);
		background: color-mix(in oklab, var(--accent), transparent 93%);
	}
	.preset input {
		position: absolute;
		opacity: 0;
		width: 1px;
		height: 1px;
	}
	.preset:has(input:focus-visible) {
		box-shadow: var(--focus-ring);
	}
	.pl {
		font-family: var(--font-mono);
		font-weight: 600;
		font-size: var(--text-sm);
		color: var(--fg);
	}
	.pk {
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--fg-quiet);
		align-self: center;
	}
	.pb {
		grid-column: 1 / -1;
		font-size: var(--text-xs);
		color: var(--fg-2);
	}
	.row2 {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
		gap: var(--space-4);
	}
	.row3 {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
		gap: var(--space-4);
	}
	textarea {
		width: 100%;
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-sm);
		border: 1px solid var(--border);
		background: var(--surface);
		color: var(--fg);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
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
	.sign {
		padding-top: var(--space-5);
	}
	.adv {
		border-top: 1px solid var(--border-soft);
		padding-top: var(--space-3);
	}
	summary {
		cursor: pointer;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--fg-2);
		width: fit-content;
	}
	summary:hover {
		color: var(--fg);
	}
	.adv-body {
		display: grid;
		gap: var(--space-4);
		margin-top: var(--space-4);
	}
	.seg {
		display: inline-flex;
		width: fit-content;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		overflow: hidden;
	}
	.seg label {
		position: relative;
		padding: var(--space-1) var(--space-3);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--fg-2);
		cursor: pointer;
	}
	.seg label + label {
		border-left: 1px solid var(--border);
	}
	.seg label.on {
		background: var(--surface-raised);
		color: var(--accent-text);
	}
	.seg input {
		position: absolute;
		opacity: 0;
		width: 1px;
		height: 1px;
	}
	.seg label:has(input:focus-visible) {
		box-shadow: inset var(--focus-ring);
	}
	.toggles {
		gap: var(--space-5);
		padding: var(--space-4);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}
	.ferr {
		color: var(--danger-text);
		font-size: var(--text-sm);
	}
	.submit {
		display: flex;
		align-items: center;
		gap: var(--space-4);
		flex-wrap: wrap;
	}
	.note {
		font-size: var(--text-xs);
		font-family: var(--font-mono);
	}
</style>
