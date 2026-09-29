<script>
  import PageHeader from '../../lib/components/PageHeader.svelte'
  import Card from '../../lib/components/Card.svelte'
  import StatTile from '../../lib/components/StatTile.svelte'
  import StatusBadge from '../../lib/components/StatusBadge.svelte'
  import Chart from '../../lib/components/Chart.svelte'
  import TierMix from '../../lib/components/TierMix.svelte'
  import RequestsTable from '../../lib/components/RequestsTable.svelte'
  import ErrorBox from '../../lib/components/ErrorBox.svelte'
  import { api } from '../../lib/api.js'
  import { num, tokens, ms, cost, pct, isoTime } from '../../lib/format.js'

  let data = $state(null)
  let error = $state('')

  $effect(() => {
    let timer
    let stop = false
    const load = async () => {
      try {
        data = await api.get('/api/admin/overview')
        error = ''
      } catch (e) {
        error = e
      }
      if (!stop) timer = setTimeout(load, 5000)
    }
    load()
    return () => {
      stop = true
      clearTimeout(timer)
    }
  })

  // Live points are 5-second intervals; show rates per minute.
  const live = $derived(data?.live ?? [])
  const x = $derived(live.map((p) => p.t))
  const perMin = (v) => v * 12
  const t = $derived(data?.today?.totals)
  const routerNote = $derived.by(() => {
    const sy = data?.switchyard
    if (!sy) return ''
    const parts = []
    if (sy.started_at) parts.push(`since ${isoTime(sy.started_at)}`)
    if (sy.restarts) parts.push(`${sy.restarts} crash restart${sy.restarts === 1 ? '' : 's'}`)
    return parts.join(' · ')
  })
  const lastHour = $derived.by(() => {
    const sum = (k) => live.reduce((a, p) => a + p[k], 0)
    const lat = live.filter((p) => p.latency_ms > 0)
    return {
      requests: sum('requests'),
      errors: sum('errors'),
      latency: lat.length ? lat.reduce((a, p) => a + p.latency_ms, 0) / lat.length : null,
      overhead: live.filter((p) => p.routing_overhead_ms > 0).reduce((a, p, _, arr) => a + p.routing_overhead_ms / arr.length, 0) || null,
    }
  })
</script>

<PageHeader title="Dashboard" subtitle="Live view of the router and today's usage." />
<ErrorBox {error} />

{#if data}
  <div class="stack">
    {#if !data.configured}
      <div class="alert info">The router isn't set up yet. <a href="/config">Run the setup wizard</a> to add providers, models and routes.</div>
    {/if}

    <div class="grid-tiles">
      <StatTile label="Router" note={routerNote}>
        <StatusBadge status={data.switchyard.state} />
      </StatTile>
      <StatTile label="Requests, last hour" value={num(lastHour.requests)} note={lastHour.errors ? `${num(lastHour.errors)} failed` : 'none failed'} />
      <StatTile label="Avg. latency, last hour" value={ms(lastHour.latency)} note="full turn, request to last token" />
      <StatTile label="Routing overhead" value={ms(lastHour.overhead)} note="time spent choosing a model" />
      <StatTile label="Tokens today" value={tokens(t.prompt_tokens + t.completion_tokens)} note={`${tokens(t.cached_tokens)} from cache`} />
      <StatTile label="Estimated cost today" value={cost(t.cost, t.currency)} note={t.cost === null ? 'unknown until every model used has a price' : 'estimate'} />
    </div>

    <div class="grid">
      <Card title="Requests per minute" subtitle="Last hour, live">
        <Chart {x} label="Requests per minute" series={[{ label: 'Requests/min', values: live.map((p) => perMin(p.requests)) }]} format={(v) => num(Math.round(v))} />
      </Card>
      <Card title="Tokens per minute" subtitle="In and out, last hour">
        <Chart {x} label="Tokens per minute" series={[{ label: 'Tokens/min', values: live.map((p) => perMin(p.prompt_tokens + p.completion_tokens)) }]} format={tokens} />
      </Card>
      <Card title="Average latency" subtitle="Full turn, last hour">
        <Chart {x} label="Average latency" series={[{ label: 'Latency', values: live.map((p) => (p.latency_ms > 0 ? p.latency_ms : null)) }]} format={ms} />
      </Card>
      <Card title="Errors per minute" subtitle="Failed responses, last hour">
        <Chart {x} label="Errors per minute" series={[{ label: 'Errors/min', values: live.map((p) => perMin(p.errors)), color: 'series-2' }]} format={(v) => num(Math.round(v))} />
      </Card>
    </div>

    <div class="grid">
      <Card title="Tier mix today">
        <TierMix byTier={data.today.by_tier} />
      </Card>
      <Card title="Top users today" flush>
        <table class="data">
          <thead><tr><th>User</th><th class="num">Requests</th><th class="num">Tokens</th><th class="num">Share</th></tr></thead>
          <tbody>
            {#each data.top_users as u}
              <tr>
                <td><a href={`/usage?user=${encodeURIComponent(u.key)}`}>{u.key || '—'}</a></td>
                <td class="num">{num(u.requests)}</td>
                <td class="num">{tokens(u.prompt_tokens + u.completion_tokens)}</td>
                <td class="num">{pct(u.requests, t.requests)}</td>
              </tr>
            {:else}
              <tr><td colspan="4" class="muted">No requests today.</td></tr>
            {/each}
          </tbody>
        </table>
      </Card>
    </div>

    <Card title="Recent errors" subtitle="Last 7 days" flush>
      <RequestsTable rows={data.errors} showUser />
    </Card>
  </div>
{/if}
