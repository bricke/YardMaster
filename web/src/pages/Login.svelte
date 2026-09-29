<script>
  import AuthCard from '../lib/components/AuthCard.svelte'
  import ErrorBox from '../lib/components/ErrorBox.svelte'
  import { api } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'

  let username = $state('')
  let password = $state('')
  let error = $state('')
  let busy = $state(false)

  async function submit(e) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      session.user = (await api.post('/api/login', { username, password })).user
    } catch (err) {
      error = err
    }
    busy = false
  }
</script>

{#if session.authMode === 'proxy'}
  <AuthCard title="Sign in through your portal" subtitle="YardMaster is behind another proxy. Open it through that portal to sign in." />
{:else}
  <AuthCard title="Sign in">
    <form onsubmit={submit} class="stack">
      <label class="field"><span class="label">Username</span><input bind:value={username} autocomplete="username" required /></label>
      <label class="field"><span class="label">Password</span><input type="password" bind:value={password} autocomplete="current-password" required /></label>
      <ErrorBox {error} />
      <button class="btn primary" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
      <p class="muted small">Forgot your password? Ask your YardMaster admin to reset it.</p>
    </form>
  </AuthCard>
{/if}

<style>
  .small { margin: 0; }
  button { width: 100%; justify-content: center; }
</style>
