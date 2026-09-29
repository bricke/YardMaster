<script>
  // Validate → review the diff → apply with drain, health check and automatic rollback.
  // `candidate` is { model } from the wizard or { toml } from the editor.
  import Card from './Card.svelte'
  import ErrorBox from './ErrorBox.svelte'
  import { api } from '../api.js'

  let { candidate, onapplied } = $props()
  let preview = $state(null)
  let checking = $state(false)
  let applying = $state(false)
  let result = $state(null)
  let error = $state('')

  async function check() {
    checking = true
    error = ''
    try {
      preview = await api.post('/api/admin/deployment/preview', candidate)
    } catch (e) {
      error = e
    }
    checking = false
  }

  // Re-check whenever the candidate's content changes.
  let lastKey = ''
  let timer
  $effect(() => {
    const key = JSON.stringify(candidate)
    if (key === lastKey) return
    lastKey = key
    preview = null
    result = null
    clearTimeout(timer)
    timer = setTimeout(check, 400)
  })

  async function apply() {
    applying = true
    error = ''
    result = null
    try {
      await api.post('/api/admin/deployment/apply', candidate)
      result = { ok: true }
      onapplied?.()
      check()
    } catch (e) {
      result = { ok: false, reason: e.message, detail: e.body?.apply_error }
    }
    applying = false
  }
</script>

<Card title="Review and apply">
  {#snippet actions()}<button class="btn small" onclick={check} disabled={checking}>{checking ? 'Checking…' : 'Check again'}</button>{/snippet}
  <div class="stack">
    <ErrorBox {error} />
    {#if !preview}
      <p class="muted">Checking the config with switchyard-server --dry-run…</p>
    {:else if preview.errors}
      <div class="alert critical"><strong>Can't apply yet.</strong>{'\n'}{preview.errors}</div>
    {:else if !preview.changed}
      <div class="alert good">✓ Valid, and identical to the running config. Nothing to apply.</div>
    {:else}
      <div class="alert good">✓ Switchyard accepts this config.</div>
      <div>
        <p class="label">Changes against the running config</p>
        <div class="diff" aria-label="Config changes">
          {#each preview.diff as l}<div class:add={l.op === '+'} class:del={l.op === '-'}>{l.op} {l.text}</div>{/each}
        </div>
      </div>
      <div class="alert warning">Applying restarts the router. Requests in progress get up to the shutdown timeout (30 s by default) to finish; new ones wait a few seconds. If the new config doesn't come up healthy, the previous one is restored automatically.</div>
      <div class="row">
        <button class="btn primary" onclick={apply} disabled={applying}>{applying ? 'Applying… (draining and restarting)' : 'Apply config'}</button>
      </div>
    {/if}

    {#if result?.ok}
      <div class="alert good">✓ Applied. The router is running the new config.</div>
    {:else if result}
      <div class="alert critical">
        <strong>{result.reason}</strong>
        {#if result.detail?.rolled_back}{'\n'}The previous config was restored and is running.{/if}
      </div>
      {#if result.detail?.logs?.length}
        <p class="label">Last lines from switchyard-server</p>
        <pre>{result.detail.logs.join('\n')}</pre>
      {/if}
    {/if}
  </div>
</Card>
