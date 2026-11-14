<script lang="ts">
	import type { Snippet } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { session } from '$lib/session.svelte';
	import { toasts, errorText } from '$lib/toasts.svelte';
	import Logo from './Logo.svelte';
	import Dropdown from './Dropdown.svelte';
	import RoleChip from './RoleChip.svelte';
	import type { DropdownOption } from '$lib/dropdown';
	import { permFor } from '$lib/perms';
	import { stackConfig } from '$lib/stackconfig.svelte';
	import Notice from './Notice.svelte';

	let { children }: { children: Snippet } = $props();

	const nav: { group: string; items: { href: string; label: string; soon?: boolean }[] }[] = [
		{
			group: 'Server',
			items: [
				{ href: '/', label: 'Stack' },
				{ href: '/services', label: 'Services' },
				{ href: '/settings', label: 'Network settings' },
				{ href: '/config', label: 'Stack config' },
				{ href: '/directory', label: 'Directory' },
				{ href: '/audit', label: 'Audit log' }
			]
		},
		{
			group: 'Accounts',
			items: [
				{ href: '/users', label: 'Users' },
				{ href: '/roles', label: 'Roles' },
				{ href: '/invites', label: 'Invites' },
				{ href: '/sso', label: 'Sign-in providers' }
			]
		}
	];

	// Only the pages this account may open.
	let shown = $derived(
		nav
			.map((g) => ({
				...g,
				items: g.items.filter((i) => {
					const p = permFor(i.href);
					return !p || session.can(p);
				})
			}))
			.filter((g) => g.items.length)
	);

	let menuOpen = $state(false);
	type Theme = 'system' | 'dark' | 'light';
	let theme = $state<Theme>('system');
	const themes: DropdownOption<Theme>[] = [
		{ value: 'system', label: 'System', description: 'Follow the device setting' },
		{ value: 'dark', label: 'Dark', description: 'Terminal' },
		{ value: 'light', label: 'Light', description: 'Daylight' }
	];

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

	function saveTheme() {
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

	// A config file broken by a hand edit: say so on every page, re-checked
	// on navigation (at most every 15 s) and every minute.
	let configChecked = 0;
	function checkConfig(force = false) {
		if (!session.info?.features.config_file) return;
		if (!force && Date.now() - configChecked < 15000) return;
		configChecked = Date.now();
		void stackConfig.load();
	}
	$effect(() => {
		void page.url.pathname;
		checkConfig();
	});
	$effect(() => {
		const t = setInterval(() => checkConfig(true), 60000);
		return () => clearInterval(t);
	});
	let configError = $derived(session.info?.features.config_file ? stackConfig.view?.error : undefined);
</script>

<a class="skip" href="#main">Skip to content</a>
<div class="shell">
	<header class="rail">
		<div class="top">
			<a class="brand" href="/" aria-label="boson admin, home">
				<span class="mark"><Logo /></span>
				<span class="word">Boson</span>
				<span class="sub">admin</span>
			</a>
			<button
				class="menu btn btn-small"
				aria-expanded={menuOpen}
				aria-controls="nav"
				onclick={() => (menuOpen = !menuOpen)}>{menuOpen ? 'Close' : 'Menu'}</button
			>
		</div>
		<nav id="nav" class:open={menuOpen} aria-label="Main">
			{#each shown as g (g.group)}
				<div class="group" id="nav-{g.group}">{g.group}</div>
				<ul aria-labelledby="nav-{g.group}">
					{#each g.items as item (item.href)}
						<li>
							<a href={item.href} aria-current={active(item.href) ? 'page' : undefined}>
								{item.label}
								{#if item.soon}<span class="soon">Soon</span>{/if}
							</a>
						</li>
					{/each}
				</ul>
			{/each}
			<div class="foot">
				{#if session.info}
					<div class="who">
						<span class="label">Signed in as</span>
						{#if session.can('users.view')}
							<a href="/users/{session.info.user.id}">{session.info.user.username}</a>
						{:else}
							<span class="me">{session.info.user.username}</span>
						{/if}
						{#if session.info.roles.length}
							<span class="my-roles">
								{#each session.info.roles.slice(0, 3) as r (r.id)}<RoleChip role={r} size="small" />{/each}
							</span>
						{/if}
					</div>
				{/if}
				<div class="row">
					<div class="theme">
						<span class="label" id="theme-label">Theme</span>
						<Dropdown
							options={themes}
							bind:value={theme}
							onchange={saveTheme}
							labelledby="theme-label"
							size="small"
						/>
					</div>
					<button class="btn btn-small" onclick={signOut}>Sign out</button>
				</div>
				{#if session.info}
					<span class="ver">Panel {session.info.features.version}</span>
				{/if}
			</div>
		</nav>
	</header>
	<main id="main" tabindex="-1">
		{#if configError && page.url.pathname !== '/config'}
			<div class="cfg-banner">
				<Notice tone="bad">
					<strong
						>config/boson.yaml has an error{configError.line ? ` on line ${configError.line}` : ''}:</strong
					>
					{configError.message}. Services keep their last good settings until it’s fixed.
					<a href="/config#raw-h">Show the file</a>
				</Notice>
			</div>
		{/if}
		{@render children()}
	</main>
</div>

<style>
	.cfg-banner {
		margin-bottom: var(--space-6);
		max-width: 760px;
	}
	.skip {
		position: absolute;
		left: -9999px;
	}
	.skip:focus {
		left: var(--space-4);
		top: var(--space-4);
		z-index: 100;
		background: var(--surface-raised);
		color: var(--fg);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
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
			var(--border) calc(var(--rail) - 1px) var(--rail),
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
		border-right: 1px solid var(--border);
		padding: var(--space-5) var(--space-4) var(--space-4);
	}
	.top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
	}
	.brand {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		color: var(--fg);
		text-decoration: none;
		padding: var(--space-1) var(--space-3);
		margin-left: calc(-1 * var(--space-1));
	}
	.brand:hover {
		color: var(--fg);
	}
	.mark {
		display: inline-flex;
		color: var(--accent);
	}
	.word {
		font-family: var(--font-display);
		font-size: 17px;
		font-weight: 600;
		letter-spacing: var(--tracking-display);
	}
	.sub {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--fg-quiet);
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
	.group {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--fg-quiet);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		padding: var(--space-4) var(--space-3) var(--space-2);
	}
	.group:first-child {
		padding-top: 0;
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
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-sm);
		border-left: 2px solid transparent;
		color: var(--fg-2);
		text-decoration: none;
		font-family: var(--font-display);
		font-size: var(--text-sm);
		font-weight: 600;
		letter-spacing: var(--tracking-display);
		transition:
			background-color var(--motion-fast) var(--ease-standard),
			color var(--motion-fast) var(--ease-standard),
			border-color var(--motion-fast) var(--ease-standard);
	}
	nav a:hover {
		color: var(--fg);
		background: var(--surface-hover);
	}
	nav a[aria-current='page'] {
		color: var(--accent-text);
		background: var(--surface-raised);
		border-left-color: var(--accent);
		border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
	}
	.soon {
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: 500;
		color: var(--fg-quiet);
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
	}
	.foot {
		margin-top: auto;
		display: grid;
		gap: var(--space-3);
		padding: var(--space-4) var(--space-3) 0;
		border-top: 1px solid var(--border-soft);
		font-size: var(--text-sm);
	}
	.who {
		display: grid;
		gap: 2px;
	}
	.label {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	nav .who a {
		display: inline;
		padding: 0;
		border: 0;
		background: none;
		font-family: var(--font-mono);
		font-size: var(--text-base);
		color: var(--fg);
		font-weight: 600;
		text-decoration: none;
		overflow-wrap: anywhere;
	}
	.me {
		font-family: var(--font-mono);
		font-size: var(--text-base);
		font-weight: 600;
		color: var(--fg);
		overflow-wrap: anywhere;
	}
	.my-roles {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
		margin-top: var(--space-1);
	}
	nav .who a:hover {
		color: var(--accent-text);
		background: none;
	}
	.row {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: var(--space-3);
	}
	.theme {
		display: grid;
		gap: 2px;
		width: 100%;
	}
	.ver {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	main {
		padding: var(--space-10) clamp(16px, 4vw, 56px) var(--space-12);
		max-width: 1280px;
		width: 100%;
		outline: none;
		box-shadow: none;
	}

	@media (max-width: 820px) {
		.shell {
			grid-template-columns: minmax(0, 1fr);
			grid-template-rows: auto 1fr;
			background: none;
		}
		.rail {
			position: sticky;
			height: auto;
			z-index: 20;
			padding: var(--space-3) var(--space-4);
			border-right: 0;
			border-bottom: 1px solid var(--border);
			background: color-mix(in oklab, var(--surface) 92%, transparent);
			backdrop-filter: blur(12px);
		}
		.menu {
			display: inline-flex;
		}
		nav {
			display: none;
			margin-top: var(--space-4);
		}
		nav.open {
			display: flex;
		}
		.foot {
			margin-top: var(--space-4);
			padding-bottom: var(--space-2);
		}
		main {
			padding-top: var(--space-6);
		}
	}
</style>
