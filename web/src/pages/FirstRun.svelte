<script>
  import AuthCard from '../lib/components/AuthCard.svelte'
  import ErrorBox from '../lib/components/ErrorBox.svelte'
  import { api } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'

  let code = $state('')
  let username = $state('admin')
  let password = $state('')
  let again = $state('')
  let error = $state('')
  let busy = $state(false)

  async function submit(e) {
    e.preventDefault()
    if (password !== again) {
      error = "The passwords don't match."
      return
    }
    busy = true
    error = ''
    try {
      session.user = (await api.post('/api/first-run', { setup_code: code, username, password })).user
      session.firstRun = false
    } catch (err) {
      error = err
    }
    busy = false
  }
</script>

<AuthCard title="Create the admin account" subtitle="First start. Only someone who can read the container log can do this.">
  <form onsubmit={submit} class="stack">
    <label class="field">
      <span class="label">Setup code</span>
      <input bind:value={code} placeholder="XXXX-XXXX-XXXX" autocomplete="off" required />
      <span class="hint">Printed in the container log: <code>docker logs yardmaster</code></span>
    </label>
    <label class="field"><span class="label">Admin username</span><input bind:value={username} autocomplete="username" required /></label>
    <label class="field">
      <span class="label">Password</span>
      <input type="password" bind:value={password} autocomplete="new-password" minlength="10" required />
      <span class="hint">At least 10 characters.</span>
    </label>
    <label class="field"><span class="label">Password again</span><input type="password" bind:value={again} autocomplete="new-password" required /></label>
    <ErrorBox {error} />
    <button class="btn primary" disabled={busy}>{busy ? 'Creating…' : 'Create admin account'}</button>
  </form>
</AuthCard>

<style>
  button { width: 100%; justify-content: center; }
</style>
