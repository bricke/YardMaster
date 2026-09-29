<script>
  import AuthCard from '../lib/components/AuthCard.svelte'
  import ErrorBox from '../lib/components/ErrorBox.svelte'
  import { api } from '../lib/api.js'
  import { session, signOut } from '../lib/session.svelte.js'

  // forced: shown full-screen after signing in with a temporary password.
  let { forced = false } = $props()
  let current = $state('')
  let next = $state('')
  let again = $state('')
  let error = $state('')
  let done = $state(false)
  let busy = $state(false)

  async function submit(e) {
    e.preventDefault()
    if (next !== again) {
      error = "The new passwords don't match."
      return
    }
    busy = true
    error = ''
    try {
      session.user = (await api.post('/api/password', { current, new: next })).user
      done = true
      current = next = again = ''
    } catch (err) {
      error = err
    }
    busy = false
  }
</script>

{#snippet form()}
  <form onsubmit={submit} class="stack">
    <label class="field">
      <span class="label">{forced ? 'Temporary password' : 'Current password'}</span>
      <input type="password" bind:value={current} autocomplete="current-password" required />
    </label>
    <label class="field">
      <span class="label">New password</span>
      <input type="password" bind:value={next} autocomplete="new-password" minlength="10" required />
      <span class="hint">At least 10 characters. Your other sessions will be signed out.</span>
    </label>
    <label class="field"><span class="label">New password again</span><input type="password" bind:value={again} autocomplete="new-password" required /></label>
    <ErrorBox {error} />
    {#if done && !forced}<div class="alert good">Password changed.</div>{/if}
    <div class="row">
      <button class="btn primary" disabled={busy}>{busy ? 'Saving…' : 'Change password'}</button>
      {#if forced}<button type="button" class="btn" onclick={signOut}>Sign out</button>{/if}
    </div>
  </form>
{/snippet}

{#if forced}
  <AuthCard title="Choose your password" subtitle="You signed in with a temporary password from your admin. Choose your own to continue.">
    {@render form()}
  </AuthCard>
{:else}
  {@render form()}
{/if}
