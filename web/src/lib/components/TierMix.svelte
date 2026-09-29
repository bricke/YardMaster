<script>
  // Share of tokens served by the strong and weak tiers: one 100% bar with direct labels,
  // a legend and a table-like row of numbers, so no value depends on colour alone.
  import { tokens as fmt, pct } from '../format.js'
  let { byTier } = $props()

  const rows = $derived.by(() => {
    const t = (key) => {
      const r = byTier.find((x) => x.key === key)
      return r ? r.prompt_tokens + r.completion_tokens : 0
    }
    return [
      { key: 'strong', label: 'Strong tier', value: t('strong'), color: 'series-1' },
      { key: 'weak', label: 'Efficient tier', value: t('weak'), color: 'series-2' },
    ]
  })
  const total = $derived(rows.reduce((a, r) => a + r.value, 0))
</script>

{#if total === 0}
  <p class="muted">No traffic on routes with a strong and an efficient tier yet. Passthrough and random routes have no tier.</p>
{:else}
  <div class="bar" role="img" aria-label="Tokens by tier">
    {#each rows as r}
      {#if r.value > 0}
        <div class="seg" style="flex:{r.value};background:var(--{r.color})" title="{r.label}: {fmt(r.value)} tokens ({pct(r.value, total)})"></div>
      {/if}
    {/each}
  </div>
  <div class="legend">
    {#each rows as r}
      <div class="item">
        <span class="key" style="background:var(--{r.color})"></span>
        <span>{r.label}</span>
        <strong>{pct(r.value, total)}</strong>
        <span class="muted">{fmt(r.value)} tokens</span>
      </div>
    {/each}
  </div>
{/if}

<style>
  .bar { display: flex; gap: 2px; height: 22px; border-radius: 4px; overflow: hidden; background: var(--card); }
  .seg { min-width: 3px; }
  .legend { display: flex; flex-wrap: wrap; gap: 8px 24px; margin-top: 10px; font-size: 13px; }
  .item { display: flex; align-items: center; gap: 6px; }
  .key { width: 10px; height: 10px; border-radius: 2px; }
</style>
