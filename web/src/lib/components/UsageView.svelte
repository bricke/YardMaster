<script>
  // Usage for one person or everyone: headline numbers, daily charts, tier mix and
  // breakdowns by route and model. Costs appear only where prices are set.
  import Card from './Card.svelte'
  import StatTile from './StatTile.svelte'
  import Chart from './Chart.svelte'
  import TierMix from './TierMix.svelte'
  import { num, tokens, cost, pct } from '../format.js'

  let { summary } = $props()
  const t = $derived(summary.totals)
  const x = $derived(summary.by_day.map((d) => Date.parse(d.key + 'T00:00:00Z') / 1000))
  const hasCost = $derived(t.cost !== null && t.cost !== undefined)
</script>

<div class="stack">
  <div class="grid-tiles">
    <StatTile label="Requests" value={num(t.requests)} note={t.errors ? `${num(t.errors)} failed (${pct(t.errors, t.requests)})` : 'none failed'} />
    <StatTile label="Tokens in" value={tokens(t.prompt_tokens)} note={t.cached_tokens ? `${tokens(t.cached_tokens)} from cache` : ''} />
    <StatTile label="Tokens out" value={tokens(t.completion_tokens)} />
    <StatTile label="Kept off the strong model" value={tokens(summary.kept_off_strong)} note="tokens served by the efficient tier" />
    <StatTile label="Estimated cost" value={hasCost ? cost(t.cost, t.currency) : '—'} note={hasCost ? 'estimate from your prices' : 'unknown until every model used has a price'} />
  </div>

  {#if summary.days > 1}
    <div class="grid">
      <Card title="Requests per day">
        <Chart {x} kind="bars" label="Requests per day" series={[{ label: 'Requests', values: summary.by_day.map((d) => d.requests) }]} format={num} />
      </Card>
      <Card title="Tokens per day" subtitle="In and out">
        <Chart {x} kind="bars" label="Tokens per day" series={[{ label: 'Tokens', values: summary.by_day.map((d) => d.prompt_tokens + d.completion_tokens) }]} format={tokens} />
      </Card>
    </div>
  {/if}

  <Card title="Tier mix" subtitle="How much traffic the router kept on the efficient tier">
    <TierMix byTier={summary.by_tier} />
  </Card>

  <div class="grid">
    {#each [['By route', summary.by_route, 'Route'], ['By model', summary.by_model, 'Model']] as [title, rows, col]}
      <Card {title} flush>
        <div class="table-wrap">
          <table class="data">
            <thead><tr><th>{col}</th><th class="num">Requests</th><th class="num">Tokens in</th><th class="num">Tokens out</th><th class="num">Cost</th></tr></thead>
            <tbody>
              {#each rows as r}
                <tr>
                  <td><code>{r.key || '—'}</code></td>
                  <td class="num">{num(r.requests)}</td>
                  <td class="num">{tokens(r.prompt_tokens)}</td>
                  <td class="num">{tokens(r.completion_tokens)}</td>
                  <td class="num">{cost(r.cost, r.currency)}</td>
                </tr>
              {:else}
                <tr><td colspan="5" class="muted">No requests in this period.</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </Card>
    {/each}
  </div>
</div>
