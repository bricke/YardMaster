<script>
  // A person's own portal: their tokens, their usage per token, their requests.
  import PageHeader from '../lib/components/PageHeader.svelte'
  import Card from '../lib/components/Card.svelte'
  import Modal from '../lib/components/Modal.svelte'
  import ErrorBox from '../lib/components/ErrorBox.svelte'
  import CopyField from '../lib/components/CopyField.svelte'
  import DaysPicker from '../lib/components/DaysPicker.svelte'
  import UsageView from '../lib/components/UsageView.svelte'
  import RequestsTable from '../lib/components/RequestsTable.svelte'
  import { api } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'
  import { num, tokens, cost, ago, date } from '../lib/format.js'

  let days = $state(30)
  let tokenList = $state([])
  let max = $state(25)
  let usage = $state(null)
  let error = $state('')

  let createOpen = $state(false)
  let name = $state('')
  let expires = $state(0)
  let created = $state(null)
  let busy = $state(false)
  let formError = $state('')
  let revoking = $state(null)

  const proxyMode = $derived(session.authMode === 'proxy')
  const expired = (t) => t.expires_at && t.expires_at * 1000 < Date.now()
  const active = $derived(tokenList.filter((t) => !t.revoked_at && !expired(t)))
  const perToken = $derived(Object.fromEntries((usage?.by_token || []).map((r) => [r.key, r])))

  async function load() {
    try {
      const [t, u] = await Promise.all([api.get('/api/me/tokens'), api.get(`/api/me/usage?days=${days}`)])
      tokenList = t.tokens
      max = t.max
      usage = u
      error = ''
    } catch (e) {
      error = e
    }
  }

  $effect(() => {
    days
    load()
  })

  function openCreate() {
    name = ''
    expires = 0
    created = null
    formError = ''
    createOpen = true
  }

  async function create(e) {
    e.preventDefault()
    busy = true
    formError = ''
    try {
      created = await api.post('/api/me/tokens', { name, expires_in_days: Number(expires) })
      load()
    } catch (err) {
      formError = err
    }
    busy = false
  }

  async function revoke() {
    busy = true
    try {
      await api.del(`/api/me/tokens/${revoking.id}`)
      revoking = null
      load()
    } catch (err) {
      error = err
    }
    busy = false
  }

  function tokenState(t) {
    if (t.revoked_at) return ['critical', 'Revoked']
    if (expired(t)) return ['warning', 'Expired']
    return ['good', 'Active']
  }
</script>

<PageHeader title={session.user.role === 'admin' ? 'My tokens' : 'Tokens & usage'} subtitle="Use a token as the API key in your agent or SDK. Only you and the admin can see your usage.">
  {#snippet actions()}
    <DaysPicker bind:days />
    {#if !proxyMode}<button class="btn primary" onclick={openCreate} disabled={active.length >= max}>+ New token</button>{/if}
  {/snippet}
</PageHeader>

<ErrorBox {error} />

<div class="stack">
  {#if proxyMode}
    <div class="alert info">YardMaster is behind another proxy, which issues your API tokens. Your usage through it appears below.</div>
  {:else}
    <Card title="Your tokens" subtitle="{active.length} of {max} active" flush>
      <div class="table-wrap">
        <table class="data">
          <thead>
            <tr><th>Name</th><th>Token</th><th>Status</th><th>Last used</th><th>Expires</th><th class="num">Requests</th><th class="num">Tokens</th><th class="num">Cost</th><th></th></tr>
          </thead>
          <tbody>
            {#each tokenList as t}
              {@const u = perToken[String(t.id)]}
              {@const [kind, label] = tokenState(t)}
              <tr>
                <td><strong>{t.name}</strong></td>
                <td><code>{t.hint}…</code></td>
                <td><span class="chip {kind}">{label}</span></td>
                <td>{ago(t.last_used_at)}</td>
                <td>{t.expires_at ? date(t.expires_at) : 'never'}</td>
                <td class="num">{num(u?.requests ?? 0)}</td>
                <td class="num">{tokens(u ? u.prompt_tokens + u.completion_tokens : 0)}</td>
                <td class="num">{cost(u?.cost, u?.currency)}</td>
                <td class="num">{#if !t.revoked_at}<button class="btn small danger" onclick={() => (revoking = t)}>Revoke</button>{/if}</td>
              </tr>
            {:else}
              <tr><td colspan="9" class="muted">No tokens yet. Create one, then follow <a href="/connect">Connect an agent</a>.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    </Card>
  {/if}

  {#if usage}
    <UsageView summary={usage.summary} />
    <Card title="Recent requests" flush>
      <RequestsTable rows={usage.recent} />
    </Card>
  {/if}
</div>

<Modal title={created ? 'Token created' : 'New token'} bind:open={createOpen} locked={busy}>
  {#if created}
    <div class="alert warning">Copy this token now. It won't be shown again.</div>
    <CopyField value={created.value} secret />
    <p class="muted">Use it as the API key in your tool. <a href="/connect" onclick={() => (createOpen = false)}>See setup guides</a>.</p>
  {:else}
    <form id="new-token" onsubmit={create} class="stack">
      <label class="field">
        <span class="label">Name</span>
        <input bind:value={name} placeholder="e.g. laptop, claude-code" maxlength="64" required />
        <span class="hint">So you can tell your tokens apart.</span>
      </label>
      <label class="field">
        <span class="label">Expires</span>
        <select bind:value={expires}>
          <option value={0}>Never</option>
          <option value={30}>In 30 days</option>
          <option value={90}>In 90 days</option>
          <option value={365}>In a year</option>
        </select>
      </label>
      <ErrorBox error={formError} />
    </form>
  {/if}
  {#snippet footer()}
    {#if created}
      <button class="btn primary" onclick={() => (createOpen = false)}>Done</button>
    {:else}
      <button class="btn" onclick={() => (createOpen = false)}>Cancel</button>
      <button class="btn primary" form="new-token" disabled={busy}>Create token</button>
    {/if}
  {/snippet}
</Modal>

<Modal title="Revoke token?" open={!!revoking} onclose={() => (revoking = null)} locked={busy}>
  <p>Tools using <strong>{revoking?.name}</strong> (<code>{revoking?.hint}…</code>) will stop working at once. This can't be undone.</p>
  {#snippet footer()}
    <button class="btn" onclick={() => (revoking = null)}>Cancel</button>
    <button class="btn primary" onclick={revoke} disabled={busy}>Revoke</button>
  {/snippet}
</Modal>
