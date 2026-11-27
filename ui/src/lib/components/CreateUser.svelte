<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import type { User } from '$lib/types';
	import type { DropdownOption } from '$lib/dropdown';
	import Dialog from './Dialog.svelte';
	import Dropdown from './Dropdown.svelte';
	import CopyField from './CopyField.svelte';

	let dlg: Dialog;
	let username = $state('');
	let email = $state('');
	let role = $state<'user' | 'admin'>('user');
	let how = $state<'email' | 'password'>('email');
	let password = $state('');
	let busy = $state(false);
	let error = $state('');
	// Email isn't set up on auth: the set-password link to pass on by hand.
	let setupLink = $state('');
	let created = $state<User | null>(null);
	let { emailOff = false }: { emailOff?: boolean } = $props();
	const uid = $props.id();

	const roles: DropdownOption<'user' | 'admin'>[] = [
		{ value: 'user', label: 'User', description: 'Chat and voice; no panel access' },
		{ value: 'admin', label: 'Admin', description: 'This panel, the admin API and voice moderation' }
	];

	export function open() {
		username = email = password = error = setupLink = '';
		created = null;
		role = 'user';
		how = 'email';
		dlg.open();
	}

	async function submit(e: Event) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			const r = await api<{ user: User; invite_email_sent?: boolean; setup_link?: string }>(
				'POST',
				'/users',
				{
					username: username.trim(),
					email: email.trim() || undefined,
					role,
					...(how === 'email' ? { send_invite_email: true } : { password })
				}
			);
			if (r.setup_link) {
				// Shown in the dialog until they copy it.
				setupLink = r.setup_link;
				created = r.user;
				return;
			}
			toasts.push(
				r.invite_email_sent
					? `Created ${r.user.username} and emailed them a link to set a password.`
					: how === 'email'
						? `Created ${r.user.username}, but the set-password email couldn’t be queued. Send a password reset from their page.`
						: `Created ${r.user.username}. Give them the password over a channel you trust.`,
				r.invite_email_sent || how === 'password' ? 'ok' : 'bad'
			);
			dlg.close();
			goto(`/users/${encodeURIComponent(r.user.id)}`);
		} catch (err) {
			error = errorText(err);
		} finally {
			busy = false;
		}
	}
</script>

<Dialog title={created ? `Created ${created.username}` : 'Create a user'} bind:this={dlg}>
	{#if created}
		<div class="cu" data-testid="setup-link">
			<p class="done">
				Email isn’t set up, so no email went out. Give {created.username} this link to choose their password, over
				a channel you trust: anyone with it can set the password until it expires or is used.
			</p>
			<CopyField label="Set-password link" value={setupLink} />
			<div class="acts">
				<button
					class="btn btn-primary"
					type="button"
					onclick={() => {
						const u = created;
						dlg.close();
						if (u) goto(`/users/${encodeURIComponent(u.id)}`);
					}}>Done</button
				>
			</div>
		</div>
	{:else}
		<form class="cu" onsubmit={submit}>
			<label class="field">
				<span>Username</span>
				<input
					type="text"
					bind:value={username}
					required
					maxlength="30"
					autocomplete="off"
					spellcheck="false"
				/>
				<small>Also their IRC nick: letters, digits and a few symbols, no spaces.</small>
			</label>
			<label class="field">
				<span>Email</span>
				<input
					type="email"
					bind:value={email}
					required={how === 'email'}
					autocomplete="off"
					spellcheck="false"
				/>
			</label>
			<div class="field">
				<span id="cu-role-{uid}">Role</span>
				<Dropdown options={roles} bind:value={role} labelledby="cu-role-{uid}" />
			</div>
			<fieldset class="how">
				<legend>First sign-in</legend>
				<label class:on={how === 'email'}>
					<input type="radio" bind:group={how} value="email" />
					{#if emailOff}
						<span
							><strong>Make a set-password link</strong>Email isn’t set up, so you get the link and pass it
							on. They choose their own password. Needs an email address.</span
						>
					{:else}
						<span
							><strong>Email a set-password link</strong>They choose their own password. Needs an email
							address.</span
						>
					{/if}
				</label>
				<label class:on={how === 'password'}>
					<input type="radio" bind:group={how} value="password" />
					<span><strong>Set a password now</strong>You pass it on; they can change it later.</span>
				</label>
			</fieldset>
			{#if how === 'password'}
				<label class="field">
					<span>Password</span>
					<input type="password" bind:value={password} required minlength="10" autocomplete="new-password" />
					<small>At least 10 characters.</small>
				</label>
			{/if}
			{#if error}<p class="err" role="alert">{error}</p>{/if}
			<div class="acts">
				<button class="btn btn-ghost" type="button" onclick={() => dlg.close()}>Cancel</button>
				<button class="btn btn-primary" disabled={busy}>{busy ? 'Creating…' : 'Create user'}</button>
			</div>
		</form>
	{/if}
</Dialog>

<style>
	.done {
		margin: 0;
		color: var(--fg-2);
	}
	.cu {
		display: grid;
		gap: var(--space-4);
	}
	input[type='email'] {
		width: 100%;
		min-height: 38px;
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-sm);
		border: 1px solid var(--border);
		background: var(--surface);
		color: var(--fg);
		font-family: var(--font-mono);
		font-size: var(--text-base);
	}
	input[type='email']:focus-visible {
		outline: none;
		border-color: var(--accent);
		box-shadow:
			0 0 0 1px var(--accent),
			0 0 0 4px var(--accent-dim);
	}
	.how {
		border: 0;
		margin: 0;
		padding: 0;
		display: grid;
		gap: var(--space-2);
	}
	legend {
		padding: 0;
		margin-bottom: var(--space-2);
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-quiet);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
	}
	.how label {
		display: flex;
		gap: var(--space-3);
		align-items: flex-start;
		padding: var(--space-3);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		cursor: pointer;
		font-size: var(--text-sm);
		color: var(--fg-2);
	}
	.how label.on {
		border-color: var(--accent);
		background: color-mix(in oklab, var(--accent), transparent 93%);
	}
	.how input {
		accent-color: var(--accent);
		margin-top: 3px;
	}
	.how strong {
		display: block;
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--fg);
	}
	.err {
		color: var(--danger-text);
		font-size: var(--text-sm);
	}
	.acts {
		display: flex;
		justify-content: flex-end;
		gap: var(--space-2);
	}
</style>
