<script>
  // Prices per model, per million tokens. Without a price, a model's cost is
  // unknown, never shown as zero.
  import PageHeader from '../../lib/components/PageHeader.svelte'
  import Card from '../../lib/components/Card.svelte'
  import Modal from '../../lib/components/Modal.svelte'
  import ErrorBox from '../../lib/components/ErrorBox.svelte'
  import { api } from '../../lib/api.js'
  import { date, cost } from '../../lib/format.js'

  let rows = $state([])
  let error = $state('')
  let editing = $state(null)
  let formError = $state('')
  let busy = $state(false)

  async function load() {
    try {
      rows = (await api.get('/api/admin/prices')).prices
    } catch (e) {
      error = e
    }
  }
  load()

  function edit(r) {
    formError = ''
    editing = { target: r.target, model_id: r.model_id, ...(r.price ?? { currency: 'USD', input: 0, cached: 0, cache_write: 0, output: 0 }) }
  }

  async function save(e) {
    e.preventDefault()
    busy = true
    formError = ''
    try {
      const { target, currency, input, cached, cache_write, output } = editing
      await api.put(`/api/admin/prices/${encodeURIComponent(target)}`, {
        currency, input: +input, cached: +cached, cache_write: +cache_write, output: +output,
      })
      editing = null
      load()
    } catch (err) {
      formError = err
    }
    busy = false
  }

  async function remove(r) {
    try {
      await api.del(`/api/admin/prices/${encodeURIComponent(r.target)}`)
      load()
    } catch (e) {
      error = e
    }
  }

  const per = (v, c) => (v === undefined ? '—' : cost(v, c))
</script>

<PageHeader title="Prices" subtitle="Optional. With prices set, usage shows estimated costs. Prices change often: check your provider's price page." />
<ErrorBox {error} />
<div class="alert info" style="margin-bottom:16px">A new price applies to requests from now on. Past requests keep the cost they were recorded with.</div>

<Card flush>
  <div class="table-wrap">
    <table class="data">
      <thead><tr><th>Model</th><th>Provider's model ID</th><th class="num">Input / 1M</th><th class="num">Cached / 1M</th><th class="num">Cache write / 1M</th><th class="num">Output / 1M</th><th>Updated</th><th></th></tr></thead>
      <tbody>
        {#each rows as r}
          <tr>
            <td><strong>{r.target}</strong></td>
            <td><code>{r.model_id || 'not in the running config'}</code></td>
            {#if r.price}
              <td class="num">{per(r.price.input, r.price.currency)}</td>
              <td class="num">{per(r.price.cached, r.price.currency)}</td>
              <td class="num">{per(r.price.cache_write, r.price.currency)}</td>
              <td class="num">{per(r.price.output, r.price.currency)}</td>
              <td>{date(r.price.updated_at)}</td>
            {:else}
              <td colspan="5" class="muted">No price: costs for this model show as unknown</td>
            {/if}
            <td class="num">
              <button class="btn small" onclick={() => edit(r)}>{r.price ? 'Edit' : 'Set price'}</button>
              {#if r.price}<button class="btn small danger" onclick={() => remove(r)}>Remove</button>{/if}
            </td>
          </tr>
        {:else}
          <tr><td colspan="8" class="muted">Apply a config first; its models appear here.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</Card>

<Modal title="Price for {editing?.target}" open={!!editing} onclose={() => (editing = null)} locked={busy}>
  {#if editing}
    <form id="price" onsubmit={save} class="stack">
      <p class="muted">Per million tokens. For a local model you run yourself, enter 0 to count it as free.</p>
      <label class="field"><span class="label">Currency</span><input bind:value={editing.currency} maxlength="3" style="width:100px" /></label>
      <div class="grid">
        <label class="field"><span class="label">Input</span><input type="number" min="0" step="any" bind:value={editing.input} /></label>
        <label class="field"><span class="label">Output</span><input type="number" min="0" step="any" bind:value={editing.output} /></label>
        <label class="field"><span class="label">Cached input</span><input type="number" min="0" step="any" bind:value={editing.cached} /></label>
        <label class="field"><span class="label">Cache write</span><input type="number" min="0" step="any" bind:value={editing.cache_write} /></label>
      </div>
      <ErrorBox error={formError} />
    </form>
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (editing = null)}>Cancel</button>
    <button class="btn primary" form="price" disabled={busy}>Save price</button>
  {/snippet}
</Modal>

<style>
  td.num { white-space: nowrap; }
  .grid { grid-template-columns: repeat(auto-fit, minmax(min(100%, 140px), 1fr)); }
</style>
