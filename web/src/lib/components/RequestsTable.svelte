<script>
  import { tokens, ms, cost, dateTime, DASH } from '../format.js'
  let { rows, showUser = false } = $props()
</script>

<div class="table-wrap">
  <table class="data">
    <thead>
      <tr>
        <th>When</th>
        {#if showUser}<th>User</th>{/if}
        <th>Token</th><th>Route</th><th>Model</th><th>Tier</th>
        <th class="num">In</th><th class="num">Out</th><th class="num">Time</th><th>Status</th><th class="num">Cost</th>
      </tr>
    </thead>
    <tbody>
      {#each rows as r}
        <tr>
          <td class="nowrap">{dateTime(r.created_at)}</td>
          {#if showUser}<td>{r.user_name || DASH}</td>{/if}
          <td>{r.token_name || DASH}</td>
          <td><code>{r.route || DASH}</code></td>
          <td><code>{r.model || DASH}</code></td>
          <td>{r.tier || DASH}</td>
          <td class="num">{tokens(r.prompt_tokens)}</td>
          <td class="num">{tokens(r.completion_tokens)}</td>
          <td class="num">{ms(r.latency_ms)}</td>
          <td>
            {#if r.status === null || r.status === undefined}<span class="muted">{DASH}</span>
            {:else if r.status < 400}<span class="chip good">✓ {r.status}</span>
            {:else}<span class="chip critical" title={r.error}>✕ {r.status}</span>{/if}
          </td>
          <td class="num">{cost(r.cost, r.currency)}</td>
        </tr>
      {:else}
        <tr><td colspan={showUser ? 11 : 10} class="muted">No requests yet.</td></tr>
      {/each}
    </tbody>
  </table>
</div>
