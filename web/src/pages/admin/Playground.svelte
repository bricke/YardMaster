<script>
  // Ask the router which model it would pick for a message (POST /v1/decision), without
  // the answer call. Classifier and judge calls still run and cost tokens.
  import PageHeader from '../../lib/components/PageHeader.svelte'
  import Card from '../../lib/components/Card.svelte'
  import ErrorBox from '../../lib/components/ErrorBox.svelte'
  import { api } from '../../lib/api.js'
  import { ms } from '../../lib/format.js'

  let routes = $state([])
  let route = $state('')
  let message = $state('Refactor this function to remove the duplicated error handling.')
  let result = $state(null)
  let error = $state('')
  let busy = $state(false)

  api.get('/api/connect').then(
    (d) => {
      routes = d.routes ?? []
      route = routes[0] ?? ''
    },
    (e) => (error = e),
  )

  async function run(e) {
    e.preventDefault()
    busy = true
    error = ''
    result = null
    try {
      result = await api.post('/api/admin/playground', { route, message })
    } catch (err) {
      error = err
    }
    busy = false
  }

  const decision = $derived(result?.result)
  const selected = $derived(decision?.selected ?? decision?.target ?? decision?.selected_target)
  const fallbacks = $derived(decision?.fallbacks ?? decision?.fallback_targets ?? [])
</script>

<PageHeader title="Playground" subtitle="See which model a route picks for a message, without generating an answer." />

<div class="stack">
  <div class="alert info">Routes with a judge or classifier still call it here, which costs tokens. These calls are recorded in usage as <code>yardmaster-playground</code>.</div>
  <Card>
    <form onsubmit={run} class="stack">
      <label class="field"><span class="label">Route</span>
        <select bind:value={route} required>{#each routes as r}<option>{r}</option>{/each}</select>
      </label>
      <label class="field"><span class="label">Message</span><textarea rows="5" bind:value={message} required></textarea></label>
      <ErrorBox {error} />
      <div><button class="btn primary" disabled={busy || !route}>{busy ? 'Asking the router…' : 'Show decision'}</button></div>
    </form>
  </Card>

  {#if result}
    <Card title="Decision" subtitle={`HTTP ${result.status} in ${ms(result.elapsed_ms)}`}>
      {#if result.status >= 400}
        <ErrorBox error={decision?.error?.message ?? 'The router refused the request.'} />
      {:else}
        {#if selected}
          <p>Selected: <strong><code>{selected.model ?? selected.id ?? JSON.stringify(selected)}</code></strong></p>
        {/if}
        {#if fallbacks.length}
          <p>Fallbacks, in order: {#each fallbacks as f, i}<code>{f.model ?? f.id ?? JSON.stringify(f)}</code>{i < fallbacks.length - 1 ? ' → ' : ''}{/each}</p>
        {/if}
      {/if}
      <details open={!selected}>
        <summary class="muted">Full response</summary>
        <pre>{JSON.stringify(decision, null, 2)}</pre>
      </details>
    </Card>
  {/if}
</div>
