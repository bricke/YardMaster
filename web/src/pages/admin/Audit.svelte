<script>
  // Who did what. Entries name what changed, never secret values.
  import PageHeader from '../../lib/components/PageHeader.svelte'
  import Card from '../../lib/components/Card.svelte'
  import ErrorBox from '../../lib/components/ErrorBox.svelte'
  import { api } from '../../lib/api.js'
  import { dateTime } from '../../lib/format.js'

  let entries = $state([])
  let actor = $state('')
  let action = $state('')
  let error = $state('')
  let more = $state(false)

  const kinds = [
    ['', 'All actions'], ['login', 'Sign-ins'], ['password', 'Passwords'], ['user', 'Users'],
    ['token', 'Tokens'], ['key', 'API keys'], ['config', 'Config'], ['switchyard', 'Router'],
    ['price', 'Prices'], ['https', 'HTTPS'], ['admin', 'Admin account'],
  ]

  async function load(before = 0) {
    const q = new URLSearchParams({ limit: '100' })
    if (actor) q.set('actor', actor)
    if (action) q.set('action', action)
    if (before) q.set('before', String(before))
    try {
      const r = (await api.get('/api/admin/audit?' + q)).entries
      entries = before ? [...entries, ...r] : r
      more = r.length === 100
      error = ''
    } catch (e) {
      error = e
    }
  }

  $effect(() => {
    actor
    action
    load()
  })
</script>

<PageHeader title="Audit log" subtitle="Security-relevant actions, kept for a year.">
  {#snippet actions()}
    <input placeholder="Filter by user" bind:value={actor} style="width:180px" aria-label="Filter by user" />
    <select bind:value={action} style="width:auto" aria-label="Filter by action">{#each kinds as [v, l]}<option value={v}>{l}</option>{/each}</select>
  {/snippet}
</PageHeader>
<ErrorBox {error} />

<Card flush>
  <div class="table-wrap">
    <table class="data">
      <thead><tr><th>When</th><th>Who</th><th>Action</th><th>Details</th><th>From</th></tr></thead>
      <tbody>
        {#each entries as e}
          <tr>
            <td class="nowrap">{dateTime(e.created_at)}</td>
            <td>{e.actor || '—'}</td>
            <td><code class:bad={e.action.endsWith('failed')}>{e.action}</code></td>
            <td><div class="detail">{e.detail}</div></td>
            <td class="muted nowrap">{e.ip}</td>
          </tr>
        {:else}
          <tr><td colspan="5" class="muted">No entries.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</Card>
{#if more}<button class="btn" style="margin-top:12px" onclick={() => load(entries.at(-1).id)}>Load older</button>{/if}

<style>
  .detail { white-space: pre-wrap; font-family: var(--mono); font-size: 12px; max-height: 8em; overflow: auto; }
  .bad { color: var(--critical); }
</style>
