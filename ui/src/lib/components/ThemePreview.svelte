<!--
	ThemePreview: a mock Boson client drawn with a network theme's CSS
	variables (the ones the client sets: every token, radius, fonts and
	--pane-<name>-bg / -image / -dim / -blur / -position), so admins see
	the rail, channel list, messages, members and the sign-in screen as
	people will. Variables are set through the CSSOM (cssVars), never a
	style attribute.
-->
<script lang="ts">
	import { cssVars, nickColor, themeVars, type ImageSlot, type NetworkTheme } from '$lib/theme';

	let {
		theme,
		images = {},
		network = 'Your network',
		view = 'client'
	}: {
		theme: NetworkTheme;
		images?: Partial<Record<ImageSlot, string>>;
		network?: string;
		view?: 'client' | 'login';
	} = $props();

	let vars = $derived(themeVars(theme, images));
	let initial = $derived((network.trim()[0] ?? 'N').toUpperCase());
	let font = $derived(theme.font ?? 'boson');

	const servers = [
		{ name: 'Boson', short: 'B' },
		{ name: 'Libera', short: 'L' },
		{ name: 'Rust', short: 'R' }
	];
	const channels = ['general', 'announcements', 'help', 'off-topic'];
	const messages = [
		{ nick: 'ada', time: '09:41', text: 'Morning! The new theme looks great.' },
		{ nick: 'grace', time: '09:42', text: 'Agreed. Is the voice room open for the call later?' },
		{
			nick: 'linus',
			time: '09:44',
			text: 'Yes, Lounge at 15:00.',
			mention: true
		},
		{ nick: 'ada', time: '09:45', text: 'Perfect, see you there.' }
	];
	const members = [
		{ nick: 'grace', role: 'Admins', status: 'online' },
		{ nick: 'ada', role: 'Online', status: 'online' },
		{ nick: 'linus', role: 'Online', status: 'idle' },
		{ nick: 'margaret', role: 'Online', status: 'online' }
	];
</script>

<div class="preview" class:system={font === 'system'} data-base={theme.base} use:cssVars={vars}>
	{#if view === 'client'}
		<div class="client" aria-label="Preview of the Boson app with this theme" role="img">
			<aside class="pane servers">
				<div class="layer img" aria-hidden="true"></div>
				<div class="layer dim" aria-hidden="true"></div>
				<div class="content rail">
					<span class="tile home" title="Home">⌂</span>
					<span class="sep"></span>
					<span class="tile active" title={network}>
						{#if images.logo}<img src={images.logo} alt="" />{:else}{initial}{/if}
					</span>
					{#each servers as s (s.name)}
						<span class="tile" title={s.name}>{s.short}</span>
					{/each}
					<span class="tile add" title="Add a server">+</span>
				</div>
			</aside>
			<aside class="pane channels">
				<div class="layer img" aria-hidden="true"></div>
				<div class="layer dim" aria-hidden="true"></div>
				<div class="content">
					<header class="net">
						{#if images.logo_wide}
							<img class="wide" src={images.logo_wide} alt="" />
						{:else}
							{#if images.logo}<img class="mark" src={images.logo} alt="" />{/if}
							<span class="net-name">{network}</span>
						{/if}
					</header>
					<p class="group">Text channels</p>
					{#each channels as c, i (c)}
						<p class="chan" class:active={i === 0} class:unread={i === 2}>
							<span class="hash">#</span>{c}
						</p>
					{/each}
					<p class="group">Voice</p>
					<p class="chan"><span class="hash">◖</span>lounge</p>
					<footer class="me">
						<span class="av" style:--n={nickColor('you', theme)}>Y</span>
						<span class="me-name">you<small>online</small></span>
					</footer>
				</div>
			</aside>
			<section class="pane messages">
				<div class="layer img" aria-hidden="true"></div>
				<div class="layer dim" aria-hidden="true"></div>
				<div class="content msgs">
					<header class="topic">
						<strong># general</strong>
						<span>Welcome to {network}. Be kind.</span>
					</header>
					<ol class="list">
						<li class="day"><span>Today</span></li>
						{#each messages as m, i (i)}
							<li class="msg" class:mention={m.mention}>
								<span class="av" style:--n={nickColor(m.nick, theme)}>{m.nick[0].toUpperCase()}</span>
								<div>
									<p class="who">
										<span class="nick" style:--n={nickColor(m.nick, theme)}>{m.nick}</span>
										<time>{m.time}</time>
									</p>
									<p class="text">
										{#if m.mention}<span class="at">@you</span>{/if}
										{m.text}
									</p>
								</div>
							</li>
						{/each}
						<li class="sys"><span class="ok">●</span> margaret joined</li>
					</ol>
					<div class="composer">
						<span>Message #general</span>
						<span class="send">Send</span>
					</div>
				</div>
			</section>
			<aside class="pane members">
				<div class="layer img" aria-hidden="true"></div>
				<div class="layer dim" aria-hidden="true"></div>
				<div class="content">
					<p class="group">Admins — 1</p>
					{#each members.filter((m) => m.role === 'Admins') as m (m.nick)}
						<p class="member">
							<span class="av sm" style:--n={nickColor(m.nick, theme)}>{m.nick[0].toUpperCase()}</span>
							<span class="admin">{m.nick}</span>
						</p>
					{/each}
					<p class="group">Online — 3</p>
					{#each members.filter((m) => m.role !== 'Admins') as m (m.nick)}
						<p class="member">
							<span class="av sm" style:--n={nickColor(m.nick, theme)}>{m.nick[0].toUpperCase()}</span>
							<span>{m.nick}</span>
							{#if m.status === 'idle'}<span class="idle" title="Idle">●</span>{/if}
						</p>
					{/each}
				</div>
			</aside>
		</div>
	{:else}
		<div class="login" aria-label="Preview of the sign-in screen with this theme" role="img">
			{#if images.login_background}
				<img class="login-bg" src={images.login_background} alt="" />
			{/if}
			<div class="login-dim" class:has={!!images.login_background}></div>
			<div class="card">
				<div class="brand">
					{#if images.logo}
						<img src={images.logo} alt="" />
					{:else}
						<span class="tile active">{initial}</span>
					{/if}
				</div>
				<h3>Sign in to {network}</h3>
				<p class="sub">Use your {network} account.</p>
				<div class="input"><span>Username</span><b>ada</b></div>
				<div class="input"><span>Password</span><b>••••••••••</b></div>
				<span class="primary">Sign in</span>
				<p class="links">
					<span class="link">Forgot password?</span> · <span class="link">Create an account</span>
				</p>
				<p class="err">Wrong username or password.</p>
			</div>
		</div>
	{/if}
</div>

<style>
	.preview {
		/* set by cssVars; these keep the component sane before it runs */
		--bg: #070709;
		--fg: #e2e2ea;
		container-type: inline-size;
		color: var(--fg);
		background: var(--bg);
		font-family: var(--font-body);
		font-size: 13px;
		line-height: 1.45;
		border-radius: var(--radius-lg);
		overflow: hidden;
		border: 1px solid var(--border);
		isolation: isolate;
	}
	.preview p {
		margin: 0;
	}

	/* ---- client ---- */
	.client {
		display: grid;
		grid-template-columns: 60px 176px minmax(0, 1fr) 150px;
		height: 440px;
	}
	@container (max-width: 680px) {
		.client {
			grid-template-columns: 52px 140px minmax(0, 1fr) 120px;
		}
	}
	@container (max-width: 520px) {
		.client {
			grid-template-columns: 52px 140px minmax(0, 1fr);
		}
		.members {
			display: none;
		}
	}
	@container (max-width: 440px) {
		.client {
			grid-template-columns: 52px minmax(0, 1fr);
		}
		.channels {
			display: none;
		}
	}
	.pane {
		position: relative;
		overflow: hidden;
		min-width: 0;
	}
	.layer {
		position: absolute;
		inset: 0;
		pointer-events: none;
	}
	.servers {
		--pb: var(--pane-servers-bg);
		--pi: var(--pane-servers-image);
		--pd: var(--pane-servers-dim);
		--px: var(--pane-servers-blur);
		--pp: var(--pane-servers-position);
		border-right: 1px solid var(--border-soft);
	}
	.channels {
		--pb: var(--pane-channels-bg);
		--pi: var(--pane-channels-image);
		--pd: var(--pane-channels-dim);
		--px: var(--pane-channels-blur);
		--pp: var(--pane-channels-position);
		border-right: 1px solid var(--border-soft);
	}
	.messages {
		--pb: var(--pane-messages-bg);
		--pi: var(--pane-messages-image);
		--pd: var(--pane-messages-dim);
		--px: var(--pane-messages-blur);
		--pp: var(--pane-messages-position);
	}
	.members {
		--pb: var(--pane-members-bg);
		--pi: var(--pane-members-image);
		--pd: var(--pane-members-dim);
		--px: var(--pane-members-blur);
		--pp: var(--pane-members-position);
		border-left: 1px solid var(--border-soft);
	}
	.pane {
		background: var(--pb);
	}
	.layer.img {
		background-image: var(--pi);
		background-size: cover;
		background-position: var(--pp);
		filter: blur(var(--px));
		/* blur pulls the edges in: overscan so they stay covered */
		inset: calc(-2 * var(--px));
	}
	.layer.dim {
		background: var(--pb);
		opacity: var(--pd);
	}
	.content {
		position: relative;
		height: 100%;
		display: flex;
		flex-direction: column;
		min-height: 0;
	}

	.rail {
		align-items: center;
		gap: 8px;
		padding: 10px 0;
	}
	.tile {
		width: 38px;
		height: 38px;
		flex: none;
		display: grid;
		place-items: center;
		border-radius: var(--radius-lg);
		background: var(--surface-raised);
		color: var(--fg-2);
		font-family: var(--font-display);
		font-weight: 700;
		font-size: 14px;
		overflow: hidden;
	}
	.tile.active {
		background: var(--accent);
		color: var(--accent-on);
		box-shadow:
			0 0 0 2px var(--pb, var(--bg)),
			0 0 0 3px var(--accent);
	}
	.tile img {
		width: 100%;
		height: 100%;
		object-fit: cover;
	}
	.tile.home {
		color: var(--fg);
	}
	.tile.add {
		background: transparent;
		color: var(--success);
		border: 1px dashed var(--border);
	}
	.sep {
		width: 24px;
		height: 1px;
		background: var(--border);
	}

	.channels .content,
	.members .content {
		padding: 0 8px 8px;
	}
	.net {
		display: flex;
		align-items: center;
		gap: 8px;
		height: 44px;
		margin: 0 -8px 6px;
		padding: 0 12px;
		border-bottom: 1px solid var(--border-soft);
		font-family: var(--font-display);
		font-weight: 700;
		font-size: 13px;
		flex: none;
	}
	.net .mark {
		width: 22px;
		height: 22px;
		border-radius: var(--radius-sm);
		object-fit: cover;
	}
	.net .wide {
		display: block;
		min-width: 0;
		width: 100%;
		height: 24px;
		object-fit: contain;
		object-position: left center;
	}
	.net-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.group {
		margin: 10px 6px 4px !important;
		font-family: var(--font-display);
		font-size: 10px;
		font-weight: 600;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--fg-3);
	}
	.chan {
		padding: 4px 8px;
		border-radius: var(--radius-sm);
		color: var(--fg-2);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.chan .hash {
		color: var(--fg-3);
		margin-right: 6px;
		font-family: var(--font-display);
	}
	.chan.active {
		background: var(--surface-hover);
		color: var(--fg);
	}
	.chan.unread {
		color: var(--fg);
		font-weight: 600;
	}
	.me {
		margin: auto -8px -8px;
		padding: 8px 10px;
		display: flex;
		gap: 8px;
		align-items: center;
		border-top: 1px solid var(--border-soft);
		background: color-mix(in oklab, var(--surface-raised), transparent 20%);
	}
	.me-name {
		display: grid;
		font-weight: 600;
		line-height: 1.2;
	}
	.me-name small {
		font-weight: 400;
		font-size: 11px;
		color: var(--success);
	}
	.av {
		--n: var(--accent);
		width: 32px;
		height: 32px;
		flex: none;
		display: grid;
		place-items: center;
		border-radius: var(--radius-md);
		background: color-mix(in oklab, var(--n), transparent 78%);
		color: var(--n);
		font-family: var(--font-display);
		font-weight: 700;
		font-size: 13px;
	}
	.av.sm {
		width: 24px;
		height: 24px;
		font-size: 11px;
	}

	.topic {
		display: flex;
		align-items: baseline;
		gap: 10px;
		height: 44px;
		padding: 0 16px;
		align-items: center;
		border-bottom: 1px solid var(--border-soft);
		flex: none;
		white-space: nowrap;
		overflow: hidden;
	}
	.topic strong {
		font-family: var(--font-display);
	}
	.topic span {
		color: var(--fg-3);
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.list {
		list-style: none;
		margin: 0;
		padding: 8px 0;
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}
	.day {
		display: flex;
		align-items: center;
		gap: 8px;
		margin: 4px 16px 8px;
		color: var(--fg-3);
		font-size: 11px;
		font-family: var(--font-display);
	}
	.day::before,
	.day::after {
		content: '';
		flex: 1;
		height: 1px;
		background: var(--border-soft);
	}
	.msg {
		display: flex;
		gap: 10px;
		padding: 5px 16px;
	}
	.msg.mention {
		background: color-mix(in oklab, var(--warn), transparent 90%);
		box-shadow: inset 2px 0 0 var(--warn);
	}
	.who {
		display: flex;
		gap: 8px;
		align-items: baseline;
	}
	.nick {
		color: var(--n);
		font-weight: 600;
	}
	time {
		color: var(--meta);
		font-size: 11px;
		font-family: var(--font-display);
	}
	.text {
		color: var(--fg);
	}
	.at {
		color: var(--accent);
		background: color-mix(in oklab, var(--accent), transparent 85%);
		border-radius: var(--radius-sm);
		padding: 0 3px;
		font-weight: 600;
	}
	.sys {
		padding: 4px 16px 4px 58px;
		color: var(--muted);
		font-size: 12px;
	}
	.ok {
		color: var(--success);
		font-size: 9px;
	}
	.composer {
		margin: 0 12px 12px;
		padding: 9px 12px;
		display: flex;
		justify-content: space-between;
		align-items: center;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--surface);
		color: var(--fg-3);
		flex: none;
	}
	.send {
		padding: 3px 10px;
		border-radius: var(--radius-sm);
		background: var(--accent);
		color: var(--accent-on);
		font-family: var(--font-display);
		font-weight: 600;
		font-size: 11px;
	}
	.members .content {
		padding-top: 8px;
	}
	.member {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 3px 6px;
		color: var(--fg-2);
	}
	.member .admin {
		color: var(--danger);
		font-weight: 600;
	}
	.idle {
		color: var(--warn);
		font-size: 9px;
		margin-left: auto;
	}

	/* ---- sign-in ---- */
	.login {
		position: relative;
		height: 440px;
		display: grid;
		place-items: center;
		background: var(--bg);
		overflow: hidden;
	}
	.login-bg {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		object-fit: cover;
	}
	.login-dim {
		position: absolute;
		inset: 0;
		background: radial-gradient(
			circle at 50% 0%,
			color-mix(in oklab, var(--accent), transparent 85%),
			transparent 60%
		);
	}
	.login-dim.has {
		background: color-mix(in oklab, var(--bg), transparent 45%);
	}
	.card {
		position: relative;
		width: min(320px, calc(100% - 32px));
		display: grid;
		gap: 10px;
		padding: 24px;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: 0 16px 48px rgba(0, 0, 0, 0.35);
	}
	.brand {
		display: flex;
		justify-content: center;
		margin-bottom: 2px;
	}
	.brand img {
		width: 48px;
		height: 48px;
		object-fit: contain;
		border-radius: var(--radius-md);
	}
	.brand .tile {
		width: 48px;
		height: 48px;
		font-size: 20px;
		box-shadow: none;
	}
	.card h3 {
		text-align: center;
		font-family: var(--font-display);
		font-size: 17px;
		color: var(--fg);
		margin: 0;
	}
	.sub {
		text-align: center;
		color: var(--fg-2);
		font-size: 12px;
		margin-top: -4px !important;
	}
	.input {
		display: grid;
		gap: 4px;
	}
	.input span {
		font-family: var(--font-display);
		font-size: 10px;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--fg-3);
	}
	.input b {
		font-weight: 400;
		padding: 7px 10px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--bg);
		color: var(--fg);
	}
	.primary {
		margin-top: 4px;
		padding: 8px;
		text-align: center;
		border-radius: var(--radius-sm);
		background: var(--accent);
		color: var(--accent-on);
		font-family: var(--font-display);
		font-weight: 600;
	}
	.links {
		text-align: center;
		color: var(--fg-3);
		font-size: 12px;
	}
	.link {
		color: var(--accent);
	}
	.err {
		text-align: center;
		color: var(--danger);
		font-size: 12px;
	}
</style>
