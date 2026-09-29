<script>
  // One routing strategy explained: what it does, what it costs, when to use it, its
  // settings. Used by the setup wizard's help dialog and the Help page.
  import { judgeAdvice } from '../strategies.js'
  let { strategy, showJudge = true } = $props()
</script>

<div class="detail">
  <p class="summary">{strategy.summary}</p>
  {#each strategy.how as para}<p>{para}</p>{/each}

  <dl class="facts">
    <div><dt>Models</dt><dd>{strategy.models}</dd></div>
    <div><dt>Extra LLM calls</dt><dd>{strategy.extraCalls}</dd></div>
    <div><dt>Added latency</dt><dd>{strategy.latency}</dd></div>
  </dl>

  <div class="two">
    <div>
      <h4>Good for</h4>
      <ul>{#each strategy.goodFor as g}<li>{g}</li>{/each}</ul>
    </div>
    <div>
      <h4>Avoid when</h4>
      <ul>{#each strategy.avoidWhen as a}<li>{a}</li>{/each}</ul>
    </div>
  </div>

  {#if strategy.settings.length}
    <h4>Settings</h4>
    <table class="data">
      <tbody>
        {#each strategy.settings as s}
          <tr>
            <td class="name">{s.name}</td>
            <td>{s.explain}{#if s.start}<br /><span class="muted">Start with: {s.start}</span>{/if}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  {#if showJudge && strategy.type === 'llm_classifier'}
    <h4>Choosing a judge</h4>
    <ul>{#each judgeAdvice as a}<li>{a}</li>{/each}</ul>
  {/if}

  {#if !strategy.wizard}
    <p class="muted small">Not in the setup wizard yet: configure it under Setup → Edit TOML (see Switchyard’s TOML schema).</p>
  {/if}
</div>

<style>
  .detail p { margin: 0 0 10px; }
  .summary { font-weight: 600; }
  .facts { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 180px), 1fr)); gap: 10px; margin: 14px 0; }
  .facts div { background: var(--raised); border: 1px solid var(--line); border-radius: var(--radius); padding: 8px 10px; }
  dt { font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.04em; color: var(--muted); }
  dd { margin: 2px 0 0; }
  .two { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 260px), 1fr)); gap: 16px; }
  h4 { margin: 14px 0 6px; font-size: 13px; }
  ul { margin: 0; padding-left: 18px; }
  li { margin-bottom: 4px; }
  td.name { font-weight: 600; white-space: nowrap; vertical-align: top; width: 1%; }
  .small { margin-top: 12px; }
</style>
