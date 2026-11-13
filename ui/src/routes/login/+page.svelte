<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import type { SessionInfo } from '$lib/types';
	import Logo from '$lib/components/Logo.svelte';
	import Notice from '$lib/components/Notice.svelte';

	let login = $state('');
	let password = $state('');
	let busy = $state(false);
	let error = $state('');

	function next(): string {
		const n = page.url.searchParams.get('next') ?? '/';
		// Only same-app paths, never //host or absolute URLs.
		return n.startsWith('/') && !n.startsWith('//') ? n : '/';
	}

	$effect(() => {
		if (session.info) goto(next(), { replaceState: true });
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			const s = await api<SessionInfo>('POST', '/login', { login, password });
			password = '';
			session.set(s);
		} catch (err) {
			if (err instanceof ApiError) {
				error =
					err.code === 'invalid_credentials'
						? 'That username or password is wrong.'
						: err.code === 'not_admin'
							? 'This account can sign in to boson, but it isn’t an administrator. Ask an admin to change its role.'
							: err.message;
			} else error = String(err);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>Sign in · boson admin</title></svelte:head>

<div class="wrap">
	<form class="card" onsubmit={submit} aria-describedby={error ? 'login-error' : undefined}>
		<div class="brand"><Logo /><span>boson</span><span class="muted">admin</span></div>
		<h1>Sign in to manage your server</h1>
		<p class="lede">Use your boson account. Only accounts with the admin role can open the panel.</p>

		{#if session.notice && !error}
			<Notice tone="info">{session.notice}</Notice>
		{/if}
		{#if error}
			<div id="login-error"><Notice>{error}</Notice></div>
		{/if}

		<label class="field">
			<span>Username or email</span>
			<input
				type="text"
				bind:value={login}
				autocomplete="username"
				required
				autocapitalize="none"
				spellcheck="false"
			/>
		</label>
		<label class="field">
			<span>Password</span>
			<input type="password" bind:value={password} autocomplete="current-password" required />
		</label>
		<button class="btn btn-primary" type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
		<p class="help muted">
			Forgot your password? Reset it from your boson sign-in page, or ask another admin. First admin: create
			one with <code>auth user create --role admin</code>.
		</p>
	</form>
</div>

<style>
	.wrap {
		min-height: 100vh;
		display: grid;
		place-items: center;
		padding: var(--space-4);
		background: radial-gradient(60rem 30rem at 85% -10%, var(--accent-soft), transparent 60%), var(--ink);
	}
	.card {
		width: min(400px, 100%);
		display: grid;
		gap: var(--space-4);
		padding: var(--space-6);
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius-m);
	}
	.brand {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		font-weight: 700;
		font-size: var(--t-md);
	}
	.brand .muted {
		font-weight: 400;
		font-size: var(--t-sm);
	}
	h1 {
		font-size: var(--t-lg);
		margin-top: var(--space-3);
	}
	.lede {
		color: var(--text-2);
		font-size: var(--t-sm);
		margin-top: calc(-1 * var(--space-2));
	}
	.btn {
		min-height: 40px;
		margin-top: var(--space-2);
	}
	.help {
		font-size: var(--t-xs);
	}
	@media (max-width: 480px) {
		.card {
			padding: var(--space-5) var(--space-4);
		}
	}
</style>
