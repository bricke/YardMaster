<script>
  // How routing works and what each strategy does. Open to everyone, so people can see
  // why their request went to the model it did.
  import PageHeader from '../lib/components/PageHeader.svelte'
  import Card from '../lib/components/Card.svelte'
  import StrategyDetail from '../lib/components/StrategyDetail.svelte'
  import { strategies, byType, judgeAdvice, SWITCHYARD_VERSION } from '../lib/strategies.js'
  import { api } from '../lib/api.js'

  let routes = $state([])
  api.get('/api/connect').then((d) => (routes = d.route_strategies ?? []), () => {})
</script>

<PageHeader title="How routing works" subtitle="What happens between your tool and the model that answers." />

<div class="stack">
  <Card>
    <ol class="steps">
      <li><strong>You name a route</strong> as the model in your tool, e.g. <code>smart</code>.</li>
      <li><strong>The route’s strategy picks a model</strong>, for example by reading the task or the agent’s progress.</li>
      <li><strong>That model answers.</strong> Timeouts, rate limits and server errors are retried, then the route’s other models are tried in order.</li>
    </ol>
  </Card>

  {#if routes.length}
    <Card title="Routes on this YardMaster" flush>
      <table class="data">
        <thead><tr><th>Route (model name)</th><th>Strategy</th><th>In short</th></tr></thead>
        <tbody>
          {#each routes as r}
            <tr>
              <td><code>{r.id}</code></td>
              <td><a href={`#${r.strategy}`}>{byType[r.strategy]?.name ?? r.strategy}</a></td>
              <td>{byType[r.strategy]?.summary ?? ''}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </Card>
  {/if}

  <Card title="Strategies at a glance" flush>
    <div class="table-wrap">
      <table class="data">
        <thead><tr><th>Strategy</th><th>What it does</th><th>Models</th><th>Extra LLM calls</th></tr></thead>
        <tbody>
          {#each strategies as s}
            <tr>
              <td class="nowrap"><a href={`#${s.type}`}>{s.name}</a>{#if !s.wizard}<br /><span class="muted small">TOML only</span>{/if}</td>
              <td>{s.summary}</td>
              <td>{s.models}</td>
              <td>{s.extraCalls}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </Card>

  {#each strategies as s}
    <section id={s.type}>
      <Card title={s.name} subtitle={s.wizard ? '' : 'Set up through Edit TOML'}>
        <StrategyDetail strategy={s} showJudge={false} />
      </Card>
    </section>
  {/each}

  <section id="judge">
    <Card title="Choosing a judge" subtitle="For the classifier, escalation, composite and custom strategies">
      <ul>{#each judgeAdvice as a}<li>{a}</li>{/each}</ul>
    </Card>
  </section>

  <p class="muted small">Written for Switchyard {SWITCHYARD_VERSION}, the router inside this YardMaster.</p>
</div>

<style>
  .steps { margin: 0; padding-left: 20px; }
  .steps li { margin-bottom: 6px; }
  section { scroll-margin-top: calc(var(--topbar-h) + 16px); }
  ul { margin: 0; padding-left: 18px; }
  li { margin-bottom: 6px; }
</style>
