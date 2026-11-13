<script lang="ts">
	import type { Snippet } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import Logo from './Logo.svelte';

	let { children }: { children: Snippet } = $props();

	const nav = [
		{ href: '/', label: 'Stack' },
		{ href: '/users', label: 'Users' },
		{ href: '/invites', label: 'Invites' },
		{ href: '/audit', label: 'Audit log' },
		{ href: '/sso', label: 'Sign-in providers', soon: true }
	];

	let menuOpen = $state(false);
	let theme = $state<'system' | 'dark' | 'light'>('system');

	$effect(() => {
		try {
			const t = localStorage.getItem('theme');
			if (t === 'dark' || t === 'light') theme = t;
		} catch {
			/* storage blocked */
		}
	});
	$effect(() => {
		if (theme === 'system') document.documentElement.removeAttribute('data-theme');
		else document.documentElement.dataset.theme = theme;
	});

	function cycleTheme() {
		theme = theme === 'system' ? 'dark' : theme === 'dark' ? 'light' : 'system';
		try {
			if (theme === 'system') localStorage.removeItem('theme');
			else localStorage.setItem('theme', theme);
		} catch {
			/* storage blocked */
		}
	}

	function active(href: string) {
		const p = page.url.pathname;
		return href === '/' ? p === '/' : p === href || p.startsWith(href + '/');
	}

	async function signOut() {
		try {
			await api('POST', '/logout');
		} catch (e) {
			toasts.push(errorText(e), 'bad');
		}
		session.clear('You signed out.');
		goto('/login');
	}

	$effect(() => {
		void page.url.pathname;
		menuOpen = false;
	});
</script>

<a class="skip" href="#main">Skip to content</a>
<div class="shell">
	<header class="rail">
		<div class="top">
			<a class="brand" href="/" aria-label="boson admin, stack overview">
				<Logo />
				<span class="word">boson</span>
				<span class="sub">admin</span>
			</a>
			<button
				class="menu btn btn-ghost btn-small"
				aria-expanded={menuOpen}
				aria-controls="nav"
				onclick={() => (menuOpen = !menuOpen)}>{menuOpen ? 'Close' : 'Menu'}</button
			>
		</div>
		<nav id="nav" class:open={menuOpen} aria-label="Main">
			<ul>
				{#each nav as item (item.href)}
					<li>
						<a href={item.href} aria-current={active(item.href) ? 'page' : undefined}>
							{item.label}
							{#if item.soon}<span class="soon">Soon</span>{/if}
						</a>
					</li>
				{/each}
			</ul>
			<div class="foot">
				{#if session.info}
					<div class="who">
						<span class="muted">Signed in as</span>
						<a href="/users/{session.info.user.id}">{session.info.user.username}</a>
					</div>
				{/if}
				<div class="row">
					<button class="btn btn-small" onclick={signOut}>Sign out</button>
					<button class="btn btn-small" onclick={cycleTheme} title="Colour theme">Theme: {theme}</button>
				</div>
				{#if session.info}
					<span class="ver muted">Panel {session.info.features.version}</span>
				{/if}
			</div>
		</nav>
	</header>
	<main id="main" tabindex="-1">
		{@render children()}
	</main>
</div>

<style>
	.skip {
		position: absolute;
		left: -9999px;
	}
	.skip:focus {
		left: var(--space-4);
		top: var(--space-4);
		z-index: 100;
		background: var(--raised);
		padding: var(--space-2) var(--space-3);
	}
	.shell {
		display: grid;
		grid-template-columns: var(--rail) minmax(0, 1fr);
		min-height: 100vh;
		/* the rail's ground and edge run the full page height */
		background: linear-gradient(
			90deg,
			var(--surface) calc(var(--rail) - 1px),
			var(--line) calc(var(--rail) - 1px) var(--rail),
			transparent var(--rail)
		);
	}
	.rail {
		position: sticky;
		top: 0;
		height: 100vh;
		display: flex;
		flex-direction: column;
		background: var(--surface);
		padding: var(--space-5) var(--space-4);
	}
	.top {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}
	.brand {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		color: var(--text);
		text-decoration: none;
		padding: 0 var(--space-2);
	}
	.word {
		font-weight: 700;
		font-size: var(--t-md);
		letter-spacing: -0.02em;
	}
	.sub {
		color: var(--muted);
		font-size: var(--t-sm);
	}
	.menu {
		display: none;
	}
	nav {
		display: flex;
		flex-direction: column;
		flex: 1;
		margin-top: var(--space-6);
	}
	nav ul {
		list-style: none;
		margin: 0;
		padding: 0;
		display: grid;
		gap: 2px;
	}
	nav a {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-2);
		padding: 7px var(--space-2);
		border-radius: var(--radius-s);
		color: var(--text-2);
		text-decoration: none;
		font-size: var(--t-sm);
		font-weight: 550;
		border-left: 2px solid transparent;
	}
	nav a:hover {
		color: var(--text);
		background: var(--surface-2);
	}
	nav a[aria-current='page'] {
		color: var(--text);
		background: var(--accent-soft);
		border-left-color: var(--accent);
		border-radius: 0 var(--radius-s) var(--radius-s) 0;
	}
	.soon {
		font-size: var(--t-xs);
		color: var(--muted);
		font-weight: 500;
	}
	.foot {
		margin-top: auto;
		display: grid;
		gap: var(--space-3);
		padding: var(--space-4) var(--space-2) 0;
		border-top: 1px solid var(--line);
		font-size: var(--t-sm);
	}
	.who {
		display: grid;
	}
	nav .who a {
		display: inline;
		padding: 0;
		border: 0;
		background: none;
		font-size: var(--t-base);
		color: var(--text);
		font-weight: 600;
		text-decoration: none;
		overflow-wrap: anywhere;
	}
	.row {
		display: flex;
		gap: var(--space-2);
		flex-wrap: wrap;
	}
	.ver {
		font-size: var(--t-xs);
	}
	main {
		padding: var(--space-6) clamp(16px, 4vw, 56px) var(--space-7);
		max-width: 1280px;
		width: 100%;
		outline: none;
	}

	@media (max-width: 820px) {
		.shell {
			grid-template-columns: minmax(0, 1fr);
			background: none;
		}
		.rail {
			position: sticky;
			height: auto;
			z-index: 20;
			padding: var(--space-3) var(--space-4);
			border-right: 0;
			border-bottom: 1px solid var(--line);
		}
		.menu {
			display: inline-flex;
		}
		nav {
			display: none;
			margin-top: var(--space-3);
		}
		nav.open {
			display: flex;
		}
		.foot {
			margin-top: var(--space-4);
			padding-bottom: var(--space-2);
		}
		main {
			padding-top: var(--space-5);
		}
	}
</style>
