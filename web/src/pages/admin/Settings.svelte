<script>
  // HTTPS: the name YardMaster is reached by, the built-in certificate authority,
  // and the browser check that turns HTTPS on.
  import PageHeader from '../../lib/components/PageHeader.svelte'
  import Card from '../../lib/components/Card.svelte'
  import ErrorBox from '../../lib/components/ErrorBox.svelte'
  import { api } from '../../lib/api.js'
  import { date } from '../../lib/format.js'

  let st = $state(null)
  let name = $state('')
  let error = $state('')
  let busy = $state(false)
  let checkMsg = $state('')

  async function load() {
    try {
      st = await api.get('/api/admin/https')
      name = st.name || location.hostname
    } catch (e) {
      error = e
    }
  }
  load()

  async function setName(e) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      st = await api.post('/api/admin/https/name', { name })
      await confirmName()
    } catch (err) {
      error = err
    }
    busy = false
  }

  // The plain-HTTP port as this browser sees it (it can differ from the container's port
  // when Docker maps it to another one).
  const port = () => (location.protocol === 'http:' && location.port ? location.port : st.http_port)

  // The browser's check is the verdict: it fetches this instance's ID through the name,
  // exactly as coworkers' computers will reach it.
  async function confirmName() {
    checkMsg = ''
    try {
      const r = await fetch(`http://${st.name}:${port()}/api/instance-id`, { cache: 'no-store' })
      const { id } = await r.json()
      st = await api.post('/api/admin/https/confirm', { name: st.name, instance_id: id })
      checkMsg = ''
    } catch (err) {
      checkMsg = err?.status
        ? err.message
        : `This browser couldn't reach http://${st.name}:${port()}. Make sure the name resolves to this server (DNS or hosts file), then check again.`
    }
  }
</script>

<PageHeader title="Settings" />
<ErrorBox {error} />

{#if st}
  <div class="stack">
    <Card title="HTTPS" subtitle="Encrypts passwords, tokens and prompts on the network.">
      {#if st.proxy_mode}
        <p>YardMaster runs behind another proxy, which handles HTTPS.</p>
      {:else if !st.enabled}
        <p>HTTPS is turned off (<code>YARDMASTER_TLS=off</code>).</p>
      {:else if st.mode === 'custom'}
        <p><span class="chip good">✓ HTTPS on</span> with your own certificate for <code>{st.name}</code>{st.not_after ? `, valid until ${date(st.not_after)}` : ''}.</p>
        <p class="muted">Replace <code>custom.crt</code> and <code>custom.key</code> in the tls folder to renew it; YardMaster picks it up within the hour.</p>
      {:else}
        <div class="stack">
          {#if st.active}
            <p><span class="chip good">✓ HTTPS on</span> at <a href={`https://${st.name}:${st.https_port}`}>https://{st.name}:{st.https_port}</a></p>
            <p>People install YardMaster's certificate once per computer from <a href={`http://${st.name}:${st.http_port}/setup`}>the setup page</a>. Plain HTTP now only serves that page.</p>
          {:else if st.pending}
            <p><span class="chip warning">○ Waiting for the name check</span> for <code>{st.name}</code>.</p>
            <button class="btn primary" onclick={confirmName}>Check the name and turn HTTPS on</button>
            {#if checkMsg}<div class="alert warning">{checkMsg}</div>{/if}
          {:else}
            <p><span class="chip warning">○ HTTPS off</span> Traffic on your network is readable.</p>
          {/if}

          <form onsubmit={setName} class="stack">
            <label class="field">
              <span class="label">Name people use to reach YardMaster</span>
              <input bind:value={name} placeholder="yardmaster.office.lan" required />
              <span class="hint">A DNS name that resolves to this server on your network, not an IP address. Changing it creates a new certificate authority that everyone must install again.</span>
            </label>
            <div><button class="btn" disabled={busy}>{busy ? 'Working…' : st.name ? 'Change name' : 'Set name and turn on HTTPS'}</button></div>
          </form>

          {#if st.fingerprint}
            <div>
              <p class="label">Certificate authority fingerprint (SHA-256)</p>
              <pre class="fp">{st.fingerprint}</pre>
              <p class="muted small">Also printed in the container log at every start. Compare them before trusting the certificate.</p>
            </div>
          {/if}
          <p class="muted small">Prefer your own certificate? Put <code>custom.crt</code> and <code>custom.key</code> in <code>/data/tls/</code> and restart the container.</p>
        </div>
      {/if}
    </Card>
  </div>
{/if}

<style>
  .fp { white-space: pre-wrap; word-break: break-all; }
  p { margin: 0; }
</style>
