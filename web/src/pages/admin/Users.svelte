<script>
  // Add, deactivate and reset users. No email yet: temporary passwords are
  // shown to the admin once, to hand over.
  import PageHeader from '../../lib/components/PageHeader.svelte'
  import Card from '../../lib/components/Card.svelte'
  import Modal from '../../lib/components/Modal.svelte'
  import ErrorBox from '../../lib/components/ErrorBox.svelte'
  import CopyField from '../../lib/components/CopyField.svelte'
  import { api } from '../../lib/api.js'
  import { session } from '../../lib/session.svelte.js'
  import { date, dateTime } from '../../lib/format.js'

  let users = $state([])
  let error = $state('')
  let busy = $state(false)

  let addOpen = $state(false)
  let username = $state('')
  let displayName = $state('')
  let formError = $state('')
  let handover = $state(null) // { user, password, reset }
  let confirm = $state(null) // { user, action }

  const proxyMode = $derived(session.authMode === 'proxy')

  async function load() {
    try {
      users = (await api.get('/api/admin/users')).users
    } catch (e) {
      error = e
    }
  }
  load()

  function openAdd() {
    username = displayName = formError = ''
    addOpen = true
  }

  async function add(e) {
    e.preventDefault()
    busy = true
    formError = ''
    try {
      const r = await api.post('/api/admin/users', { username, display_name: displayName })
      addOpen = false
      handover = { user: r.user, password: r.temporary_password, reset: false }
      load()
    } catch (err) {
      formError = err
    }
    busy = false
  }

  async function run() {
    const { user, action } = confirm
    busy = true
    try {
      if (action === 'reset') {
        const r = await api.post(`/api/admin/users/${user.id}/reset-password`)
        handover = { user, password: r.temporary_password, reset: true }
      } else if (action === 'delete') {
        await api.del(`/api/admin/users/${user.id}`)
      } else {
        await api.post(`/api/admin/users/${user.id}/active`, { active: action === 'activate' })
      }
      confirm = null
      load()
    } catch (err) {
      error = err
      confirm = null
    }
    busy = false
  }

  function status(u) {
    if (!u.active) return ['critical', 'Deactivated']
    if (u.must_change_password) return ['warning', 'Password not set yet']
    return ['good', 'Active']
  }
</script>

<PageHeader title="Users" subtitle="People who can sign in to manage their tokens and see their own usage.">
  {#snippet actions()}
    {#if !proxyMode}<button class="btn primary" onclick={openAdd}>+ Add user</button>{/if}
  {/snippet}
</PageHeader>
<ErrorBox {error} />
{#if proxyMode}
  <div class="alert info" style="margin-bottom:16px">YardMaster is behind another proxy: people are added there, and appear here after their first visit.</div>
{/if}

<Card flush>
  <div class="table-wrap">
    <table class="data">
      <thead><tr><th>Name</th><th>Username</th><th>Role</th><th>Status</th><th>Added</th><th></th></tr></thead>
      <tbody>
        {#each users as u}
          {@const [kind, label] = status(u)}
          <tr>
            <td><strong>{u.display_name}</strong></td>
            <td><code>{u.username}</code></td>
            <td>{u.role}</td>
            <td>
              <span class="chip {kind}">{label}</span>
              {#if u.active && u.must_change_password && u.temp_password_expires_at}
                <span class="muted small">temporary password expires {dateTime(u.temp_password_expires_at)}</span>
              {/if}
            </td>
            <td>{date(u.created_at)}</td>
            <td><div class="actions">
              <a class="btn small" href={`/usage?user=${encodeURIComponent(u.username)}`}>Usage</a>
              {#if u.role !== 'admin' && u.source === 'builtin'}
                <button class="btn small" onclick={() => (confirm = { user: u, action: 'reset' })} disabled={!u.active}>Reset password</button>
                {#if u.active}
                  <button class="btn small danger" onclick={() => (confirm = { user: u, action: 'deactivate' })}>Deactivate</button>
                {:else}
                  <button class="btn small" onclick={() => (confirm = { user: u, action: 'activate' })}>Reactivate</button>
                {/if}
              {/if}
              {#if u.role !== 'admin'}
                <button class="btn small danger" onclick={() => (confirm = { user: u, action: 'delete' })}>Delete</button>
              {/if}
            </div></td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</Card>

<Modal title="Add user" bind:open={addOpen} locked={busy}>
  <form id="add-user" onsubmit={add} class="stack">
    <label class="field">
      <span class="label">Username</span>
      <input bind:value={username} placeholder="e.g. alice" required autocomplete="off" />
      <span class="hint">Letters, digits, dots, dashes and underscores. It labels this person's requests.</span>
    </label>
    <label class="field"><span class="label">Display name</span><input bind:value={displayName} placeholder="e.g. Alice Martin" /></label>
    <ErrorBox error={formError} />
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (addOpen = false)}>Cancel</button>
    <button class="btn primary" form="add-user" disabled={busy}>Add user</button>
  {/snippet}
</Modal>

<Modal title={handover?.reset ? 'Password reset' : 'User added'} open={!!handover} onclose={() => (handover = null)}>
  {#if handover}
    <p>Give <strong>{handover.user.display_name}</strong> this temporary password. They'll choose their own when they sign in as <code>{handover.user.username}</code>.</p>
    <CopyField value={handover.password} secret />
    <div class="alert warning">It's shown only now and expires in 7 days if unused.{handover.reset ? ' Their sessions were signed out; their tokens keep working.' : ''}</div>
  {/if}
  {#snippet footer()}<button class="btn primary" onclick={() => (handover = null)}>Done</button>{/snippet}
</Modal>

<Modal title="Are you sure?" open={!!confirm} onclose={() => (confirm = null)} locked={busy}>
  {#if confirm}
    {#if confirm.action === 'reset'}
      <p>Reset the password of <strong>{confirm.user.display_name}</strong>? They'll be signed out and need the new temporary password. Their tokens keep working.</p>
    {:else if confirm.action === 'deactivate'}
      <p>Deactivate <strong>{confirm.user.display_name}</strong>? They're signed out and all their tokens stop working at once. You can reactivate them later.</p>
    {:else if confirm.action === 'delete'}
      <p>Delete <strong>{confirm.user.display_name}</strong> (<code>{confirm.user.username}</code>)? Their account, sessions and tokens are removed at once and can't be restored. Their past usage stays in the usage history, under their username.</p>
    {:else}
      <p>Reactivate <strong>{confirm.user.display_name}</strong>? Their tokens that aren't revoked or expired work again.</p>
    {/if}
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (confirm = null)}>Cancel</button>
    <button class="btn primary" onclick={run} disabled={busy}>Yes</button>
  {/snippet}
</Modal>

<style>
  .small { margin-left: 6px; }
  .actions { display: flex; gap: 6px; justify-content: flex-end; flex-wrap: wrap; }
</style>
