<script>
  // switchyard-server's health: status, restarts, its log, and its own per-model stats.
  import PageHeader from '../../lib/components/PageHeader.svelte'
  import Card from '../../lib/components/Card.svelte'
  import Modal from '../../lib/components/Modal.svelte'
  import StatusBadge from '../../lib/components/StatusBadge.svelte'
  import ErrorBox from '../../lib/components/ErrorBox.svelte'
  import { api } from '../../lib/api.js'
  import { num, tokens, ms, pct, isoTime } from '../../lib/format.js'

  let data = $state(null)
  let error = $state('')
  let confirm = $state('')
  let busy = $state(false)
  let result = $state('')

  async function load() {
    try {
      data = await api.get('/api/admin/switchyard')
      error = ''
    } catch (e) {
      error = e
    }
  }
  $effect(() => {
    load()
    const t = setInterval(load, 5000)
    return () => clearInterval(t)
  })

  async function run() {
    busy = true
    result = ''
    try {
      if (confirm === 'restart') {
        await api.post('/api/admin/switchyard/restart')
        result = 'Restarted. The router is healthy.'
      } else {
        await api.post('/api/admin/switchyard/stats-reset')
        result = "Switchyard's counters were reset. YardMaster's usage history is unchanged."
      }
    } catch (e) {
      error = e
    }
    busy = false
    confirm = ''
    load()
  }

  const models = $derived(Object.entries(data?.stats?.models ?? {}))
</script>

<PageHeader title="Router health" subtitle="The switchyard-server process YardMaster runs and supervises.">
  {#snippet actions()}
    <button class="btn" onclick={() => (confirm = 'reset')}>Reset counters</button>
    <button class="btn primary" onclick={() => (confirm = 'restart')}>Restart router</button>
  {/snippet}
</PageHeader>
<ErrorBox {error} />
{#if result}<div class="alert good" style="margin-bottom:16px">{result}</div>{/if}

{#if data}
  <div class="stack">
    <Card title="Process">
      <table class="data">
        <tbody>
          <tr><td class="muted">Status</td><td><StatusBadge status={data.status.state} /></td></tr>
          <tr><td class="muted">Switchyard version</td><td><code>{data.version}</code></td></tr>
          <tr><td class="muted">Running since</td><td>{isoTime(data.status.started_at)}</td></tr>
          <tr><td class="muted">Last healthy check</td><td>{isoTime(data.status.last_healthy)}</td></tr>
          <tr><td class="muted">Restarts after a crash</td><td>{data.status.restarts}</td></tr>
          {#if data.status.last_exit}<tr><td class="muted">Last exit</td><td><code>{data.status.last_exit}</code></td></tr>{/if}
        </tbody>
      </table>
    </Card>

    <Card title="Per model, since the last reset" subtitle="Switchyard's own counters (/v1/stats)" flush>
      <div class="table-wrap">
        <table class="data">
          <thead><tr><th>Model</th><th class="num">Calls</th><th class="num">Errors</th><th class="num">Share</th><th class="num">Tokens</th><th class="num">Cache hit rate</th><th class="num">Avg. latency</th><th class="num">p99 latency</th></tr></thead>
          <tbody>
            {#each models as [name, m]}
              <tr>
                <td><code>{name}</code></td>
                <td class="num">{num(m.calls)}</td>
                <td class="num">{num(m.errors)}</td>
                <td class="num">{pct(m.request_pct, 100)}</td>
                <td class="num">{tokens(m.total_tokens)}</td>
                <td class="num">{pct(m.cache_hit_rate, 1)}</td>
                <td class="num">{ms(m.total_latency?.avg_ms)}</td>
                <td class="num">{ms(m.total_latency?.p99_ms)}</td>
              </tr>
            {:else}
              <tr><td colspan="8" class="muted">{data.stats ? 'No calls yet.' : 'The router is not answering.'}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if data.stats?.routing_overhead}
        <p class="muted pad">Routing overhead: average {ms(data.stats.routing_overhead.avg_ms)}, p99 {ms(data.stats.routing_overhead.p99_ms)} over {num(data.stats.routing_overhead.count)} requests.</p>
      {/if}
    </Card>

    <Card title="Log" subtitle="Last lines from switchyard-server">
      <pre class="log">{data.logs.length ? data.logs.join('\n') : 'Nothing logged yet.'}</pre>
    </Card>
  </div>
{/if}

<Modal title={confirm === 'restart' ? 'Restart the router?' : 'Reset counters?'} open={!!confirm} onclose={() => (confirm = '')} locked={busy}>
  {#if confirm === 'restart'}
    <p>Requests in progress get up to the shutdown timeout to finish; new ones wait a few seconds.</p>
  {:else}
    <p>Clears Switchyard's own per-model counters. YardMaster's usage history isn't affected.</p>
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (confirm = '')}>Cancel</button>
    <button class="btn primary" onclick={run} disabled={busy}>{busy ? 'Working…' : 'Yes'}</button>
  {/snippet}
</Modal>

<style>
  .log { max-height: 420px; overflow: auto; white-space: pre-wrap; word-break: break-all; }
  .pad { padding: 10px 16px; margin: 0; font-size: 12.5px; }
</style>
