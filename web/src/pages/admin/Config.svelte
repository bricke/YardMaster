<script>
  // The setup wizard: providers → models → routes → review and apply. Advanced users
  // can edit the TOML directly, and restore a previously applied config from history.
  import { tick } from 'svelte'
  import PageHeader from '../../lib/components/PageHeader.svelte'
  import Card from '../../lib/components/Card.svelte'
  import Tabs from '../../lib/components/Tabs.svelte'
  import Modal from '../../lib/components/Modal.svelte'
  import ErrorBox from '../../lib/components/ErrorBox.svelte'
  import Stepper from '../../lib/components/Stepper.svelte'
  import ApplyPanel from '../../lib/components/ApplyPanel.svelte'
  import StrategyDetail from '../../lib/components/StrategyDetail.svelte'
  import { strategies, byType } from '../../lib/strategies.js'
  import { api } from '../../lib/api.js'
  import { dateTime } from '../../lib/format.js'
  import { suggestNames } from '../../lib/naming.js'

  let tab = $state('wizard')
  let step = $state(0)
  let model = $state({ clients: [], targets: [], routes: [] })
  let source = $state('')
  let currentToml = $state('')
  // Choices the backend validates against, so both sides use one list.
  let formats = $state([])
  let openAIEfforts = $state([])
  let anthropicEfforts = $state([])
  let keys = $state({})
  let encrypted = $state(false)
  let error = $state('')
  let loaded = $state(false)

  let keyFor = $state(null)
  let keyValue = $state('')
  let keyBusy = $state(false)
  let keyError = $state('')
  // Shown next to the provider after its key is saved: { name, text }.
  let keyNotice = $state(null)

  let rawToml = $state('')
  // The strategy shown in the help dialog, or null.
  let helpFor = $state(null)
  const wizardStrategies = strategies.filter((x) => x.wizard).map((x) => [x.type, x.name])
  let history = $state([])
  let historyView = $state(null)

  const presets = {
    // The Responses API: GPT-6 models use tools together with reasoning only there.
    openai: { name: 'openai', format: 'openai_responses', base_url: 'https://api.openai.com/v1', auth: 'key', key_env: 'OPENAI_API_KEY' },
    anthropic: { name: 'anthropic', format: 'anthropic_messages', base_url: 'https://api.anthropic.com', auth: 'key', key_env: 'ANTHROPIC_API_KEY' },
    openrouter: { name: 'openrouter', format: 'openai_chat', base_url: 'https://openrouter.ai/api/v1', auth: 'key', key_env: 'OPENROUTER_API_KEY' },
    ollama: { name: 'ollama', format: 'openai_chat', base_url: 'http://host.docker.internal:11434/v1', auth: 'none', key_env: '' },
    vllm: { name: 'vllm', format: 'openai_chat', base_url: 'http://vllm:8000/v1', auth: 'none', key_env: '' },
    custom: { name: '', format: 'openai_chat', base_url: 'https://', auth: 'key', key_env: '' },
  }

  async function load() {
    try {
      const d = await api.get('/api/admin/deployment')
      model = {
        clients: d.model?.clients ?? [],
        targets: d.model?.targets ?? [],
        routes: d.model?.routes ?? [],
      }
      source = d.source
      currentToml = d.toml
      rawToml = d.toml
      formats = d.formats
      openAIEfforts = d.openai_efforts
      anthropicEfforts = d.anthropic_efforts
      loaded = true
      await loadKeys()
    } catch (e) {
      error = e
    }
  }
  load()

  async function loadKeys() {
    const names = model.clients.filter((c) => c.auth === 'key' && c.key_env).map((c) => c.key_env)
    const k = await api.get('/api/admin/keys?names=' + encodeURIComponent(names.join(',')))
    keys = Object.fromEntries(k.keys.map((x) => [x.name, x.status]))
    encrypted = k.encrypted
  }

  async function saveDraft() {
    try {
      await api.put('/api/admin/deployment/model', $state.snapshot(model))
      await loadKeys()
    } catch (e) {
      error = e
    }
  }

  function go(n) {
    saveDraft()
    step = n
  }

  // ---- providers ----
  function addClient(preset) {
    const c = { ...presets[preset] }
    if (c.name) {
      let name = c.name
      let i = 2
      while (model.clients.some((x) => x.name === name)) name = `${c.name}${i++}`
      c.name = name
    }
    model.clients.push(c)
    revealNew()
  }

  // For "Other" providers, suggest a name and key variable from the URL as it's typed,
  // until the admin edits them (lib/naming.js).
  function suggestFromURL(c) {
    const others = model.clients.filter((x) => x !== c)
    const s = suggestNames(c.base_url, others.map((x) => x.name), others.map((x) => x.key_env))
    if (!s) return
    if (!c.name || c.name === c.suggested_name) c.name = c.suggested_name = s.name
    if (c.auth === 'key' && (!c.key_env || c.key_env === c.suggested_key)) c.key_env = c.suggested_key = s.key
  }
  // Bring a newly added provider, model or route into view, focus its first empty field
  // and flash it briefly, so adding one below the fold never looks like nothing happened.
  async function revealNew() {
    await tick()
    const items = document.querySelectorAll('.item')
    const el = items[items.length - 1]
    if (!el) return
    const smooth = !matchMedia('(prefers-reduced-motion: reduce)').matches
    el.scrollIntoView({ behavior: smooth ? 'smooth' : 'auto', block: 'center' })
    // Focus the field the admin fills in first, but only when it still needs filling.
    const field = el.querySelector('[data-first]')
    if (field && (!field.value || field.value === 'https://')) field.focus({ preventScroll: true })
    el.classList.remove('new')
    void el.offsetWidth
    el.classList.add('new')
  }

  function removeAt(list, i) {
    list.splice(i, 1)
  }

  function openKey(name) {
    keyFor = name
    keyValue = keyError = ''
  }

  async function saveKey(e) {
    e.preventDefault()
    keyBusy = true
    keyError = ''
    try {
      await saveDraft()
      const name = keyFor
      const r = await api.put(`/api/admin/keys/${encodeURIComponent(name)}`, { value: keyValue })
      keyValue = ''
      await loadKeys()
      // Close the dialog and confirm next to the provider, where the status chip updates.
      keyFor = null
      keyNotice = { name, text: r.restarted ? 'Key saved. The router restarted to load it and is healthy.' : 'Key saved.' }
      setTimeout(() => keyNotice?.name === name && (keyNotice = null), 6000)
    } catch (err) {
      keyError = err
    }
    keyBusy = false
  }

  // Look up key status as key variable names are typed.
  $effect(() => {
    const names = model.clients.map((c) => c.key_env).join(',')
    if (!loaded) return
    const t = setTimeout(() => loadKeys().catch(() => {}), 400)
    return () => clearTimeout(t)
  })

  // ---- reasoning controls ----
  // Each provider takes different fields (checked against their docs, 2026-09):
  //   OpenAI:     reasoning_effort (Chat) or reasoning.effort (Responses): none … max
  //   Anthropic:  output_config.effort on Claude 4.6+; thinking.budget_tokens on 4.5 and earlier
  //   vLLM, e.g. Qwen: chat_template_kwargs.enable_thinking on/off
  // The accepted values come from the backend (deploy.OpenAIEfforts, deploy.AnthropicEfforts).

  function isOpenAI(c) {
    return /(^|\.)(openai\.com|openrouter\.ai)$/.test(hostOf(c?.base_url))
  }
  function hostOf(url) {
    try {
      return new URL(url).hostname
    } catch {
      return ''
    }
  }
  function kindOf(t) {
    const c = model.clients.find((x) => x.name === t.client)
    if (!c) return ''
    if (c.format === 'anthropic_messages') return 'anthropic'
    return isOpenAI(c) ? 'openai' : 'selfhosted'
  }
  function clearReasoning(t) {
    delete t.reasoning_effort
    delete t.anthropic_effort
    delete t.thinking_budget
    delete t.enable_thinking
  }

  // ---- models ----
  function addTarget() {
    model.targets.push({ name: '', model_id: '', client: model.clients[0]?.name ?? '', system_prompt: '' })
    revealNew()
  }

  // ---- routes ----
  // With one model, a passthrough route to it; with more, "smart" with the auto strategy.
  function addRoute() {
    const n = model.routes.length + 1
    const single = targetNames.length === 1
    const id = single ? targetNames[0] : n === 1 ? 'smart' : `route${n}`
    const r = { id, type: single ? 'passthrough' : 'auto', target: single ? targetNames[0] : '', targets: [], weights: [], base_threshold: 0.5 }
    r.name = r.suggested_name = routeKey(id, n)
    model.routes.push(r)
    revealNew()
  }

  function routeKey(id, n) {
    return id.replace(/[^a-zA-Z0-9._-]/g, '-').replace(/^[^a-zA-Z0-9]+/, '') || `route${n}`
  }

  // Keep the internal name in step with the route name until the admin edits it.
  function syncRouteName(r) {
    if (!r.name || r.name === r.suggested_name) r.name = r.suggested_name = routeKey(r.id, model.routes.indexOf(r) + 1)
  }
  const targetNames = $derived(model.targets.map((t) => t.name).filter(Boolean))

  function toggleTarget(r, name) {
    const i = r.targets.indexOf(name)
    if (i >= 0) {
      r.targets.splice(i, 1)
      r.weights?.splice(i, 1)
    } else {
      r.targets.push(name)
      r.weights = r.weights ?? []
      r.weights.push(1)
    }
  }

  const steps = ['Providers', 'Models', 'Routes', 'Review & apply']

  async function loadHistory() {
    try {
      history = (await api.get('/api/admin/deployment/history')).history
    } catch (e) {
      error = e
    }
  }
  $effect(() => {
    if (tab === 'history') loadHistory()
  })

  async function viewHistory(h) {
    const r = await api.get(`/api/admin/deployment/history/${h.id}`)
    historyView = { ...h, toml: r.toml }
  }

  function restore() {
    rawToml = historyView.toml
    historyView = null
    tab = 'toml'
  }

  function applied() {
    load()
  }
</script>

<PageHeader title="Setup" subtitle="Providers, models and the routes your coworkers call. Changes take effect when you apply them.">
  {#snippet actions()}
    {#if currentToml}<span class="chip good">✓ A config is running</span>{:else}<span class="chip warning">○ Nothing applied yet</span>{/if}
  {/snippet}
</PageHeader>
<ErrorBox {error} />

<Tabs tabs={[['wizard', 'Wizard'], ['toml', 'Edit TOML'], ['history', 'History']]} bind:active={tab} />

{#if loaded && tab === 'wizard'}
  {#if source === 'toml'}
    <div class="alert warning" style="margin-bottom:16px">The running config was last applied as hand-edited TOML. Applying from the wizard replaces it with what's below.</div>
  {/if}
  <Stepper {steps} current={step} onselect={go} />

  {#if step === 0}
    <Card title="Providers" subtitle="Where models are served: a cloud API or a server on your network.">
      {#snippet actions()}
        <select onchange={(e) => { if (e.target.value) addClient(e.target.value); e.target.value = '' }} style="width:auto">
          <option value="">+ Add provider…</option>
          <option value="openai">OpenAI</option>
          <option value="anthropic">Anthropic</option>
          <option value="openrouter">OpenRouter</option>
          <option value="ollama">Ollama (local)</option>
          <option value="vllm">vLLM (local)</option>
          <option value="custom">Other OpenAI-compatible</option>
        </select>
      {/snippet}
      <div class="stack">
        {#each model.clients as c, i}
          <div class="item">
            <div class="fields">
              <label class="field"><span class="label">Name</span><input bind:value={c.name} placeholder="e.g. company-gateway" required />
                <span class="hint">Your label for this provider.</span></label>
              <label class="field"><span class="label">API format</span>
                <select bind:value={c.format}>{#each formats as f}<option>{f}</option>{/each}</select>
                {#if hostOf(c.base_url).endsWith('openai.com') && c.format === 'openai_chat'}<span class="hint warn">OpenAI's GPT-6 models only use tools together with reasoning through openai_responses.</span>{/if}
              </label>
              <label class="field wide"><span class="label">Base URL</span><input bind:value={c.base_url} oninput={() => suggestFromURL(c)} data-first />
                <span class="hint">Usually ends in /v1. Don't include /chat/completions.</span></label>
              <label class="field"><span class="label">API key</span>
                <select bind:value={c.auth}><option value="key">Needs an API key</option><option value="none">No key (local server)</option></select>
              </label>
              <label class="field"><span class="label">Timeout (seconds)</span>
                <input type="number" min="1" step="1" placeholder="No limit"
                  bind:value={() => (c.timeout_ms ? c.timeout_ms / 1000 : null), (v) => (c.timeout_ms = v > 0 ? Math.round(v * 1000) : undefined)} />
                <span class="hint">Longest a call may take, retries and the whole answer included. Empty means no limit.</span></label>
              {#if c.auth === 'key'}
                <label class="field"><span class="label">Key variable</span><input bind:value={c.key_env} placeholder="OPENROUTER_API_KEY" />
                  <span class="hint">A name for where the key is kept. Set the key itself below.</span></label>
              {/if}
            </div>
            <div class="row">
              {#if c.auth === 'key' && c.key_env}
                {@const st = keys[c.key_env]}
                {#if st === 'env'}
                  <span class="chip good" title="Change it in your compose file or docker run">✓ Set from container environment</span>
                {:else if st === 'ui'}
                  <span class="chip good">✓ Key set</span>
                  <button class="btn small" onclick={() => openKey(c.key_env)}>Replace key</button>
                {:else}
                  <span class="chip warning">○ Key not set</span>
                  <button class="btn small primary" onclick={() => openKey(c.key_env)}>Set key</button>
                {/if}
              {/if}
              {#if keyNotice && keyNotice.name === c.key_env}<span class="saved" role="status">✓ {keyNotice.text}</span>{/if}
              <span class="spacer"></span>
              <button class="btn small danger" onclick={() => removeAt(model.clients, i)}>Remove</button>
            </div>
          </div>
        {:else}
          <p class="muted">Add a provider to start.</p>
        {/each}
      </div>
    </Card>
    <p class="muted small">
      Keys are never shown again after you save them. {encrypted ? 'They are encrypted at rest.' : 'They are stored in a file only the container can read; set YARDMASTER_SECRET_KEY to encrypt them.'}
      A variable already set on the container is used as is.
    </p>
  {:else if step === 1}
    <Card title="Models" subtitle="The models your providers serve. Routes, in the next step, choose between them.">
      {#snippet actions()}<button class="btn small" onclick={addTarget} disabled={!model.clients.length}>+ Add model</button>{/snippet}
      <div class="stack">
        {#each model.targets as t, i}
          <div class="item">
            <div class="fields">
              <label class="field"><span class="label">Name</span><input bind:value={t.name} placeholder="e.g. qwen" data-first />
                <span class="hint">Your short label for this model.</span></label>
              <label class="field"><span class="label">Served by</span>
                <select bind:value={t.client} onchange={() => clearReasoning(t)}>{#each model.clients as c}<option value={c.name}>{c.name || '(unnamed provider)'}</option>{/each}</select>
                <span class="hint">The provider from step 1 that hosts it.</span>
              </label>
              <label class="field wide"><span class="label">Model ID</span><input bind:value={t.model_id} placeholder="e.g. qwen3.8-27b" />
                <span class="hint">Exactly as your provider names it; this is what's sent to them.</span></label>
              {@render reasoning(t)}
              <label class="field full"><span class="label">System prompt (optional)</span><input bind:value={t.system_prompt} placeholder="Added before the caller's instructions when this model answers" /></label>
            </div>
            <div class="row"><span class="spacer"></span><button class="btn small danger" onclick={() => removeAt(model.targets, i)}>Remove</button></div>
          </div>
        {:else}
          <p class="muted">Add at least two models, a capable one and an efficient one, to let the router save on easy tasks.</p>
        {/each}
      </div>
    </Card>
  {:else if step === 2}
    <Card title="Routes" subtitle="Each route decides which model answers.">
      {#snippet actions()}<button class="btn small" onclick={addRoute} disabled={!targetNames.length}>+ Add route</button>{/snippet}
      <div class="stack">
        <div class="alert info">Coworkers never call a model directly: they put a <strong>route's name</strong> in their tool's "model" setting, and the route picks the model. Even a single model needs one route.</div>
        {#each model.routes as r, i}
          <div class="item">
            <div class="fields">
              <label class="field"><span class="label">Route name callers use</span><input bind:value={r.id} oninput={() => syncRouteName(r)} placeholder="e.g. smart" data-first />
                <span class="hint">What goes in their tool's model setting.</span></label>
              <label class="field"><span class="label">Internal name</span><input bind:value={r.name} />
                <span class="hint">Only used inside the config.</span></label>
              <label class="field wide"><span class="label">Strategy</span>
                <select bind:value={r.type}>{#each wizardStrategies as [type, name]}<option value={type}>{name}</option>{/each}</select>
                <span class="hint"><button type="button" class="btn link" onclick={() => (helpFor = r.type)}>How does {byType[r.type]?.name ?? r.type} work?</button></span>
              </label>
            </div>
            <p class="muted small">{byType[r.type]?.summary}</p>
            <div class="fields">
              {#if r.type === 'auto'}
                {@render pickTarget(r, 'capable_target', 'Capable model')}
                {@render pickTarget(r, 'efficient_target', 'Efficient model')}
              {:else if r.type === 'llm_classifier'}
                {@render pickTarget(r, 'strong_target', 'Strong model')}
                {@render pickTarget(r, 'weak_target', 'Weak model')}
                {@render pickTarget(r, 'classifier_target', 'Judge model')}
                <label class="field"><span class="label">Threshold (0–1)</span>
                  <input type="number" step="0.05" min="0" max="1" bind:value={r.base_threshold} />
                  <span class="hint">Higher sends less traffic to the weak model.</span>
                </label>
                <label class="field"><span class="label">Judge runs</span>
                  <select bind:value={r.classify_trigger}>
                    <option value="">On every request</option>
                    <option value="user_turn">On each new user message</option>
                    <option value="new_session">Once per session</option>
                  </select>
                </label>
              {:else if r.type === 'passthrough'}
                {@render pickTarget(r, 'target', 'Model')}
              {:else if r.type === 'random'}
                <div class="field full">
                  <span class="label">Models and weights</span>
                  <div class="stack">
                    {#each targetNames as name}
                      {@const idx = r.targets.indexOf(name)}
                      <div class="row">
                        <label class="row"><input type="checkbox" checked={idx >= 0} onchange={() => toggleTarget(r, name)} /> <code>{name}</code></label>
                        {#if idx >= 0}<input type="number" min="0" step="1" style="width:90px" bind:value={r.weights[idx]} aria-label="Weight for {name}" />{/if}
                      </div>
                    {/each}
                  </div>
                </div>
              {/if}
            </div>
            <div class="row"><span class="spacer"></span><button class="btn small danger" onclick={() => removeAt(model.routes, i)}>Remove</button></div>
          </div>
        {:else}
          {#if targetNames.length === 1}
            <div class="row"><button class="btn primary" onclick={addRoute}>Add a route to {targetNames[0]}</button><span class="muted">Every request goes to that model.</span></div>
          {:else if targetNames.length > 1}
            <div class="row"><button class="btn primary" onclick={addRoute}>Add a "smart" route</button><span class="muted">The auto strategy: efficient model first, the capable one when needed.</span></div>
          {:else}
            <p class="muted">Add models in step 2 first.</p>
          {/if}
        {/each}
      </div>
    </Card>
  {:else}
    <ApplyPanel candidate={{ model: $state.snapshot(model) }} onapplied={applied} />
  {/if}

  <div class="row nav">
    {#if step > 0}<button class="btn" onclick={() => go(step - 1)}>← Back</button>{/if}
    <span class="spacer"></span>
    {#if step < 3}<button class="btn primary" onclick={() => go(step + 1)}>Next: {steps[step + 1]} →</button>{/if}
  </div>
{:else if loaded && tab === 'toml'}
  <div class="stack">
    <div class="alert info">For route types the wizard doesn't cover (stage_router, composite, advisor, plan_execute…). See Switchyard's TOML schema. API keys go in <code>api_key_env</code> variables; set their values in the wizard's Providers step.</div>
    <Card title="Deployment TOML">
      <textarea rows="22" bind:value={rawToml} spellcheck="false" aria-label="Deployment TOML"></textarea>
    </Card>
    <ApplyPanel candidate={{ toml: rawToml }} onapplied={applied} />
  </div>
{:else if loaded && tab === 'history'}
  <Card title="Applied configs" subtitle="The last 10 applies" flush>
    <table class="data">
      <thead><tr><th>Applied</th><th></th></tr></thead>
      <tbody>
        {#each history as h}
          <tr><td>{dateTime(h.applied_at)}</td><td class="num"><button class="btn small" onclick={() => viewHistory(h)}>View</button></td></tr>
        {:else}
          <tr><td colspan="2" class="muted">Nothing applied yet.</td></tr>
        {/each}
      </tbody>
    </table>
  </Card>
{/if}

{#snippet reasoning(t)}
  {@const kind = kindOf(t)}
  {#if kind === 'openai' || kind === 'selfhosted'}
    <label class="field"><span class="label">Reasoning effort</span>
      <select bind:value={() => t.reasoning_effort ?? '', (v) => (v ? (t.reasoning_effort = v) : delete t.reasoning_effort)}>
        <option value="">Model default</option>
        {#each openAIEfforts as e}<option>{e}</option>{/each}
      </select>
      <span class="hint">{kind === 'openai'
        ? 'Forced on every request. Models accept different values: check the model page. GPT-6 Luna and Sol use tools in Chat Completions only at none.'
        : 'Only for servers that accept OpenAI\'s reasoning_effort. vLLM-served Qwen ignores it: use Thinking.'}</span>
    </label>
  {/if}
  {#if kind === 'selfhosted'}
    <label class="field"><span class="label">Thinking</span>
      <select bind:value={() => (t.enable_thinking === undefined || t.enable_thinking === null ? '' : String(t.enable_thinking)), (v) => (v === '' ? delete t.enable_thinking : (t.enable_thinking = v === 'true'))}>
        <option value="">Model default</option>
        <option value="false">Off</option>
        <option value="true">On</option>
      </select>
      <span class="hint">For vLLM-served hybrid models such as Qwen (chat_template_kwargs.enable_thinking). A tool can still ask otherwise.</span>
    </label>
  {/if}
  {#if kind === 'anthropic'}
    <label class="field"><span class="label">Effort</span>
      <select bind:value={() => t.anthropic_effort ?? '', (v) => (v ? (t.anthropic_effort = v) : delete t.anthropic_effort)}>
        <option value="">Model default</option>
        {#each anthropicEfforts as e}<option>{e}</option>{/each}
      </select>
      <span class="hint">Claude 4.6 and later (output_config.effort). xhigh and max exist only on some models. A tool can still ask otherwise.</span>
    </label>
    <label class="field"><span class="label">Thinking budget (tokens)</span>
      <input type="number" min="1024" step="256" placeholder="Off"
        bind:value={() => t.thinking_budget ?? null, (v) => (v > 0 ? (t.thinking_budget = Math.round(v)) : delete t.thinking_budget)} />
      <span class="hint">Claude 4.5 and earlier, e.g. Haiku 4.5. At least 1024, and less than the max tokens tools send, or Anthropic refuses the request.</span>
    </label>
  {/if}
{/snippet}

{#snippet pickTarget(r, field, label)}
  <label class="field"><span class="label">{label}</span>
    <select bind:value={r[field]}>
      <option value="">Choose…</option>
      {#each targetNames as n}<option>{n}</option>{/each}
    </select>
  </label>
{/snippet}

<Modal title="Routing strategies" wide open={!!helpFor} onclose={() => (helpFor = null)}>
  {#if helpFor}
    <Tabs tabs={wizardStrategies} bind:active={helpFor} />
    <StrategyDetail strategy={byType[helpFor]} />
  {/if}
  {#snippet footer()}
    <a class="btn" href="/help/routing" onclick={() => (helpFor = null)}>All strategies, including TOML-only ones</a>
    <button class="btn primary" onclick={() => (helpFor = null)}>Close</button>
  {/snippet}
</Modal>

<Modal title="API key for {keyFor}" open={!!keyFor} onclose={() => (keyFor = null)} locked={keyBusy}>
  <form id="set-key" onsubmit={saveKey} class="stack">
    <label class="field">
      <span class="label">Key</span>
      <input type="password" bind:value={keyValue} autocomplete="off" required />
      <span class="hint">Write-only: YardMaster never shows it again. If the running config uses it, the router restarts safely to load it.</span>
    </label>
    <ErrorBox error={keyError} />
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (keyFor = null)}>Cancel</button>
    <button class="btn primary" form="set-key" disabled={keyBusy || !keyValue}>{keyBusy ? 'Saving…' : 'Save key'}</button>
  {/snippet}
</Modal>

<Modal title="Config applied {historyView ? dateTime(historyView.applied_at) : ''}" open={!!historyView} onclose={() => (historyView = null)}>
  <pre class="history">{historyView?.toml}</pre>
  {#snippet footer()}
    <button class="btn" onclick={() => (historyView = null)}>Close</button>
    <button class="btn primary" onclick={restore}>Restore…</button>
  {/snippet}
</Modal>

<style>
  .item { border: 1px solid var(--line); border-radius: var(--radius); padding: 14px; background: var(--raised); display: flex; flex-direction: column; gap: 10px; }
  .fields { display: grid; gap: 12px; grid-template-columns: repeat(auto-fit, minmax(min(100%, 180px), 1fr)); }
  .fields .wide { grid-column: span 2; }
  .fields .full { grid-column: 1 / -1; }
  @media (max-width: 600px) { .fields .wide { grid-column: auto; } }
  .small { font-size: 12.5px; margin: 0; }
  .hint.warn { color: var(--warning); }
  .saved { color: var(--good); font-size: 12.5px; font-weight: 600; }
  .item:global(.new) { animation: flash 1.6s ease-out; }
  @keyframes flash {
    from { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
    to { border-color: var(--line); box-shadow: none; }
  }
  @media (prefers-reduced-motion: reduce) { .item:global(.new) { animation: none; } }
  .nav { margin-top: 16px; }
  .history { max-height: 50vh; }
</style>
