<script>
  // Setup guides for common agents and SDKs, filled in with this YardMaster's address.
  import PageHeader from '../lib/components/PageHeader.svelte'
  import Card from '../lib/components/Card.svelte'
  import Tabs from '../lib/components/Tabs.svelte'
  import ErrorBox from '../lib/components/ErrorBox.svelte'
  import CopyField from '../lib/components/CopyField.svelte'
  import { api } from '../lib/api.js'

  let info = $state(null)
  let error = $state('')
  let tool = $state('claude')
  let route = $state('')

  $effect(() => {
    api.get('/api/connect').then(
      (d) => {
        info = d
        route = d.routes?.[0] || 'your-route'
      },
      (e) => (error = e),
    )
  })

  const KEY = 'ym_your_token'
  const tools = [
    ['claude', 'Claude Code'],
    ['codex', 'Codex CLI'],
    ['opencode', 'OpenCode'],
    ['aider', 'Aider'],
    ['ide', 'Cline / Continue'],
    ['python', 'Python SDKs'],
    ['curl', 'curl'],
  ]

  const guide = $derived.by(() => {
    if (!info) return null
    const o = info.openai_base
    const a = info.anthropic_base
    const ca = info.builtin_ca ? '\nexport NODE_EXTRA_CA_CERTS=/path/to/yardmaster-ca.crt' : ''
    const pyca = info.builtin_ca ? '\nexport SSL_CERT_FILE=/path/to/yardmaster-ca.crt' : ''
    switch (tool) {
      case 'claude':
        return {
          intro: 'Claude Code speaks the Anthropic Messages API. Set these before starting it (for example in your shell profile):',
          code: `export ANTHROPIC_BASE_URL=${a}\nexport ANTHROPIC_AUTH_TOKEN=${KEY}\nexport ANTHROPIC_MODEL=${route}\nexport ANTHROPIC_SMALL_FAST_MODEL=${route}${ca}\nclaude`,
        }
      case 'codex':
        return {
          intro: 'Add a provider to ~/.codex/config.toml, and put your token in YARDMASTER_API_KEY:',
          code: `model = "${route}"\nmodel_provider = "yardmaster"\n\n[model_providers.yardmaster]\nname = "YardMaster"\nbase_url = "${o}"\nenv_key = "YARDMASTER_API_KEY"\nwire_api = "responses"`,
          after: `export YARDMASTER_API_KEY=${KEY}${ca}\ncodex`,
        }
      case 'opencode':
        return {
          intro: 'Add a provider to opencode.json (in your project or ~/.config/opencode/):',
          code: JSON.stringify(
            {
              $schema: 'https://opencode.ai/config.json',
              provider: {
                yardmaster: {
                  npm: '@ai-sdk/openai-compatible',
                  name: 'YardMaster',
                  options: { baseURL: o, apiKey: '{env:YARDMASTER_API_KEY}' },
                  models: { [route]: { name: route } },
                },
              },
            },
            null,
            2,
          ),
          after: `export YARDMASTER_API_KEY=${KEY}${ca}\nopencode`,
        }
      case 'aider':
        return {
          intro: 'Aider uses the OpenAI-compatible API:',
          code: `export OPENAI_API_BASE=${o}\nexport OPENAI_API_KEY=${KEY}${pyca}\naider --model openai/${route}`,
        }
      case 'ide':
        return {
          intro: 'In Cline, Continue and similar extensions, choose the "OpenAI Compatible" provider and enter:',
          fields: [
            ['Base URL', o],
            ['API key', KEY],
            ['Model ID', route],
          ],
        }
      case 'python':
        return {
          intro: 'Both official SDKs work. Pick the one that matches your code:',
          code: `from openai import OpenAI\n\nclient = OpenAI(base_url="${o}", api_key="${KEY}")\nreply = client.chat.completions.create(\n    model="${route}",\n    messages=[{"role": "user", "content": "Hello"}],\n)\nprint(reply.choices[0].message.content)\n\n# or, with the Anthropic SDK:\nimport anthropic\n\nclient = anthropic.Anthropic(base_url="${a}", api_key="${KEY}")\nmsg = client.messages.create(\n    model="${route}", max_tokens=512,\n    messages=[{"role": "user", "content": "Hello"}],\n)\nprint(msg.content[0].text)`,
          after: pyca ? pyca.trim() : '',
        }
      default:
        return {
          intro: 'A quick test from a terminal:',
          code: `curl ${o}/chat/completions \\\n  -H "Authorization: Bearer ${KEY}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model": "${route}", "messages": [{"role": "user", "content": "Hello"}]}'${info.builtin_ca ? ' \\\n  --cacert yardmaster-ca.crt' : ''}`,
        }
    }
  })
</script>

<PageHeader title="Connect an agent" subtitle="Point any OpenAI- or Anthropic-compatible tool at YardMaster, using one of your tokens as the API key." />
<ErrorBox {error} />

{#if info}
  <div class="stack">
    {#if !info.https}
      <div class="alert warning">⚠ This connection isn't encrypted (plain HTTP). Tokens and prompts cross the network readable by others. Ask your admin to turn on HTTPS.</div>
    {/if}
    {#if info.builtin_ca}
      <div class="alert info">
        YardMaster uses its own certificate. Install it once per computer:
        <a href={info.ca_url}>download yardmaster-ca.crt</a> (fingerprint <code>{info.ca_fingerprint?.slice(0, 23)}…</code>).
        Many tools also need the environment variable shown in the guides below.
      </div>
    {/if}

    <Card title="Your YardMaster">
      <div class="grid">
        <label class="field"><span class="label">OpenAI-compatible base URL</span><CopyField value={info.openai_base} /></label>
        <label class="field"><span class="label">Anthropic base URL</span><CopyField value={info.anthropic_base} /></label>
      </div>
      <div class="field" style="margin-top:14px">
        <span class="label">Model (route) names you can use</span>
        {#if info.routes?.length}
          <div class="row">{#each info.routes as r}<button class="chip" class:sel={r === route} onclick={() => (route = r)}>{r}</button>{/each}</div>
        {:else}
          <p class="muted">No routes yet: the admin hasn't finished setup.</p>
        {/if}
      </div>
    </Card>

    <Card title="Setup guides">
      <Tabs tabs={tools} bind:active={tool} />
      {#if guide}
        <p>{guide.intro}</p>
        {#if guide.code}<pre>{guide.code}</pre>{/if}
        {#if guide.fields}
          <table class="data"><tbody>{#each guide.fields as [k, v]}<tr><td class="muted">{k}</td><td><code>{v}</code></td></tr>{/each}</tbody></table>
        {/if}
        {#if guide.after}<p>Then:</p><pre>{guide.after}</pre>{/if}
        <p class="muted small">Replace <code>{KEY}</code> with a token from <a href="/">your tokens</a>. Tools change their settings from time to time; if something doesn't match, check the tool's own documentation for "custom base URL".</p>
      {/if}
    </Card>
  </div>
{/if}

<style>
  .chip { cursor: pointer; background: none; font-family: var(--mono); }
  .chip.sel { border-color: var(--accent); color: var(--ink); background: var(--accent-soft); }
  .small { margin-top: 12px; }
  pre { margin: 8px 0; }
</style>
