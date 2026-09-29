<script>
  import PageHeader from '../lib/components/PageHeader.svelte'
  import Card from '../lib/components/Card.svelte'
  import ChangePassword from './ChangePassword.svelte'
  import { session } from '../lib/session.svelte.js'
  import { date } from '../lib/format.js'
</script>

<PageHeader title="Account" />
<div class="grid">
  <Card title="You">
    <table class="data">
      <tbody>
        <tr><td class="muted">Name</td><td>{session.user.display_name}</td></tr>
        <tr><td class="muted">Username</td><td><code>{session.user.username}</code></td></tr>
        <tr><td class="muted">Role</td><td>{session.user.role}</td></tr>
        <tr><td class="muted">Since</td><td>{date(session.user.created_at)}</td></tr>
      </tbody>
    </table>
  </Card>
  <Card title="Password">
    {#if session.user.source === 'builtin'}
      <ChangePassword />
    {:else}
      <p class="muted">Your sign-in is managed by the proxy in front of YardMaster.</p>
    {/if}
  </Card>
</div>
