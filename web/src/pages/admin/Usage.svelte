<script>
  // Total usage and usage per user through a filter. Admins only.
  import PageHeader from '../../lib/components/PageHeader.svelte'
  import Card from '../../lib/components/Card.svelte'
  import DaysPicker from '../../lib/components/DaysPicker.svelte'
  import UsageView from '../../lib/components/UsageView.svelte'
  import RequestsTable from '../../lib/components/RequestsTable.svelte'
  import ErrorBox from '../../lib/components/ErrorBox.svelte'
  import { api } from '../../lib/api.js'
  import { route, navigate } from '../../lib/router.svelte.js'
  import { num, tokens, cost, pct, ago } from '../../lib/format.js'

  let days = $state(30)
  let user = $state(route.query.get('user') || '')
  let errorsOnly = $state(false)
  let data = $state(null)
  let error = $state('')
  let loading = $state(false)

  $effect(() => {
    const q = new URLSearchParams({ days: String(days) })
    if (user) q.set('user', user)
    if (errorsOnly) q.set('errors', '1')
    loading = true
    api.get('/api/admin/usage?' + q).then(
      (d) => {
        data = d
        error = ''
        loading = false
      },
      (e) => {
        error = e
        loading = false
      },
    )
  })

  function pick(name) {
    user = name
    navigate(name ? `/usage?user=${encodeURIComponent(name)}` : '/usage', { replace: true })
  }

  const all = $derived(data?.by_user.reduce((a, u) => a + u.requests, 0) ?? 0)
</script>

<PageHeader title="Usage" subtitle={user ? `Usage of ${user}` : 'Everyone, and per person'}>
  {#snippet actions()}
    <select bind:value={() => user, pick} style="width:auto" aria-label="User">
      <option value="">Everyone</option>
      {#each data?.by_user ?? [] as u}<option value={u.key}>{u.key}</option>{/each}
      {#if user && !data?.by_user.some((u) => u.key === user)}<option value={user}>{user}</option>{/if}
    </select>
    <DaysPicker bind:days />
  {/snippet}
</PageHeader>
<ErrorBox {error} />

{#if data}
  <div class="stack" style:opacity={loading ? 0.6 : 1}>
    <UsageView summary={data.summary} />

    {#if !user}
      <Card title="By user" flush>
        <div class="table-wrap">
          <table class="data">
            <thead><tr><th>User</th><th class="num">Requests</th><th class="num">Failed</th><th class="num">Tokens in</th><th class="num">Tokens out</th><th class="num">Routing tokens</th><th class="num">Cost</th><th class="num">Share</th><th>Last request</th></tr></thead>
            <tbody>
              {#each data.by_user as u}
                <tr>
                  <td><button class="btn link" onclick={() => pick(u.key)}>{u.key || '—'}</button></td>
                  <td class="num">{num(u.requests)}</td>
                  <td class="num">{num(u.errors)}</td>
                  <td class="num">{tokens(u.prompt_tokens)}</td>
                  <td class="num">{tokens(u.completion_tokens)}</td>
                  <td class="num">{tokens(u.routing_tokens)}</td>
                  <td class="num">{cost(u.cost, u.currency)}</td>
                  <td class="num">{pct(u.requests, all)}</td>
                  <td>{ago(u.last_at)}</td>
                </tr>
              {:else}
                <tr><td colspan="9" class="muted">No requests in this period.</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </Card>
    {/if}

    <Card title="By month" subtitle="Monthly totals are kept after per-request rows expire" flush>
      <table class="data">
        <thead><tr><th>Month</th><th class="num">Requests</th><th class="num">Failed</th><th class="num">Tokens in</th><th class="num">Tokens out</th></tr></thead>
        <tbody>
          {#each data.monthly as m}
            <tr><td>{m.key}</td><td class="num">{num(m.requests)}</td><td class="num">{num(m.errors)}</td><td class="num">{tokens(m.prompt_tokens)}</td><td class="num">{tokens(m.completion_tokens)}</td></tr>
          {:else}
            <tr><td colspan="5" class="muted">Nothing yet.</td></tr>
          {/each}
        </tbody>
      </table>
    </Card>

    <Card title="Requests" subtitle="Latest 100" flush>
      {#snippet actions()}
        <label class="row"><input type="checkbox" bind:checked={errorsOnly} /> Failed only</label>
      {/snippet}
      <RequestsTable rows={data.recent} showUser={!user} />
    </Card>
  </div>
{/if}
