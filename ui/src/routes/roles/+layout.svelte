<script lang="ts">
	import type { Snippet } from 'svelte';
	import { page } from '$app/state';
	import { rolesState } from '$lib/roles.svelte';
	import RoleList from '$lib/components/RoleList.svelte';
	import State from '$lib/components/State.svelte';

	let { children }: { children: Snippet } = $props();
	let editing = $derived(!!page.params.id);

	$effect(() => {
		if (!rolesState.loaded) rolesState.load();
	});
</script>

<div class="page-head">
	<div>
		<h1>Roles</h1>
		<p>
			Network-wide roles. Each one is also an IRC oper class, so people see it in <code>/whois</code> and, with
			a vhost, in hostmasks. Everyone has Member.
		</p>
	</div>
</div>

{#if rolesState.unsupported}
	<State kind="unsupported" title="Roles need a newer auth service">
		<p>{rolesState.unsupported}.</p>
		<p>
			Until then there are two kinds of account: admins, who can do everything in this panel, and everyone
			else. Change it on a user’s page.
		</p>
	</State>
{:else if rolesState.error}
	<State kind="error">
		{rolesState.error}
		{#snippet action()}<button class="btn btn-small" onclick={() => rolesState.load()}>Try again</button
			>{/snippet}
	</State>
{:else if !rolesState.loaded}
	<State kind="loading" title="Loading roles…" />
{:else}
	<div class="roles" class:editing>
		<aside aria-label="All roles"><RoleList /></aside>
		<div class="pane">{@render children()}</div>
	</div>
{/if}

<style>
	.roles {
		display: grid;
		grid-template-columns: 264px minmax(0, 1fr);
		gap: var(--space-8);
		align-items: start;
	}
	aside {
		position: sticky;
		top: var(--space-6);
	}
	.pane {
		min-width: 0;
	}
	@media (max-width: 980px) {
		.roles {
			grid-template-columns: minmax(0, 1fr);
		}
		aside {
			position: static;
		}
		/* phones: the list, or the role being edited */
		.editing aside {
			display: none;
		}
		.roles:not(.editing) .pane {
			display: none;
		}
	}
</style>
