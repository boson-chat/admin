<script lang="ts">
	import { rolesState } from '$lib/roles.svelte';
	import State from '$lib/components/State.svelte';
	let custom = $derived(rolesState.roles.filter((r) => !r.builtin).length);
</script>

<svelte:head><title>Roles · boson admin</title></svelte:head>

<State kind="empty" title="Pick a role to edit it">
	<p>
		Owner, Admin, Moderator and Member come with boson and can’t be deleted.
		{#if custom === 0}Create a role for anything else, like bot operators or event hosts.{:else}This network
			has
			{custom}
			{custom === 1 ? 'role' : 'roles'} of its own.{/if}
	</p>
	<p>You can change and assign only the roles below your own highest role.</p>
</State>
