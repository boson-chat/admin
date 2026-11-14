<!--
	WhoisPreview: what an IRC user sees, drawn as a classic mIRC status
	window (its own fixed palette in both themes, because that is what mIRC
	looks like). Shows /whois for a holder of the role and the join line
	with their nick!user@host.
-->
<script lang="ts">
	let {
		nick,
		host,
		whois,
		oper,
		server = 'irc.boson.chat',
		channel = '#boson'
	}: {
		nick: string;
		/** The host part users see: a vhost, or the server's cloak. */
		host: string;
		whois: string;
		oper: boolean;
		server?: string;
		channel?: string;
	} = $props();

	let n = $derived(nick || 'nick');
	let user = $derived(n.toLowerCase().slice(0, 10));
</script>

<figure class="mirc" aria-label="Preview in mIRC">
	<div class="title" aria-hidden="true">
		<span class="ico"></span>
		<span>Status: {n} [+iwx] on boson ({server}:6697)</span>
		<span class="ctl"><i></i><i></i><i></i></span>
	</div>
	<div class="win">
		<p class="sys">*** You typed <b>/whois {n}</b></p>
		<p>{n} is {user}@{host} * {n}</p>
		<p>{n} on {channel}</p>
		<p>{n} using {server} boson IRC network</p>
		{#if oper && whois}
			<p class="oper">{n} {whois}</p>
		{:else if whois}
			<p class="off">{n} {whois} <span class="why">(not shown: holders aren’t opered up)</span></p>
		{/if}
		<p>{n} is logged in as {n}</p>
		<p>{n} End of /WHOIS list.</p>
		<p class="join">* Joins: {n} ({user}@{host})</p>
	</div>
	<figcaption class="mask">
		<span class="lbl">nick!user@host</span>
		<code>{n}!{user}@{host}</code>
	</figcaption>
</figure>

<style>
	.mirc {
		margin: 0;
		display: grid;
		gap: var(--space-2);
		min-width: 0;
	}
	.title,
	.win {
		/* mIRC's own colours, not the panel's */
		font-family: 'Fixedsys Excelsior', var(--font-mono);
	}
	.title {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 5px 8px;
		background: #f0f0f0;
		color: #1a1a1a;
		border: 1px solid #a0a0a0;
		border-bottom: 0;
		border-radius: 3px 3px 0 0;
		font-family: 'Segoe UI', var(--font-body);
		font-size: 12px;
		white-space: nowrap;
		overflow: hidden;
	}
	.title span:nth-child(2) {
		overflow: hidden;
		text-overflow: ellipsis;
		min-width: 0;
	}
	.ico {
		flex: none;
		width: 12px;
		height: 12px;
		border-radius: 2px;
		background: linear-gradient(135deg, #2a6fd6 50%, #f2c230 50%);
	}
	.ctl {
		margin-left: auto;
		display: flex;
		gap: 10px;
		flex: none;
	}
	.ctl i {
		width: 9px;
		height: 9px;
		border: 1px solid #444;
	}
	.ctl i:first-child {
		height: 0;
		border-width: 0 0 1px;
		align-self: center;
	}
	.win {
		padding: 8px 10px;
		background: #ffffff;
		color: #000000;
		border: 1px solid #a0a0a0;
		border-radius: 0 0 3px 3px;
		font-size: 13px;
		line-height: 1.45;
		overflow-x: auto;
	}
	.win p {
		margin: 0;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.sys {
		color: #7f7f7f;
	}
	.sys b {
		font-weight: 600;
	}
	/* mIRC colour 2 (navy) for the line the role adds */
	.oper {
		color: #00007f;
		font-weight: 700;
	}
	.off {
		color: #7f7f7f;
		text-decoration: line-through;
		text-decoration-color: #b0b0b0;
	}
	.why {
		text-decoration: none;
		display: inline-block;
		font-style: italic;
	}
	/* mIRC colour 3 (green) for joins */
	.join {
		color: #009300;
		margin-top: 6px !important;
	}
	.mask {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-1) var(--space-3);
		font-size: var(--text-sm);
	}
	.lbl {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--fg-quiet);
	}
	.mask code {
		overflow-wrap: anywhere;
	}
	@media (forced-colors: active) {
		.win,
		.title {
			forced-color-adjust: auto;
		}
	}
</style>
