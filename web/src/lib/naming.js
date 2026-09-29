// Suggested names for a provider from its base URL, e.g.
//   https://api.mistral.ai/v1                   → mistral,        MISTRAL_API_KEY
//   https://llm-gateway.example.com/v1          → llm-gateway,    LLM_GATEWAY_API_KEY
// Generic host parts (api, www, …) are skipped, and names already taken by another
// provider get a number, so two providers never share a key variable by accident.

const generic = new Set(['api', 'apis', 'www', 'gateway', 'gw', 'llm', 'inference', 'v1', 'eu', 'us', 'prod'])

export function suggestNames(url, takenNames = [], takenKeys = []) {
  let host
  try {
    host = new URL(url).hostname
  } catch {
    return null
  }
  if (!host || /^[\d.]+$/.test(host) || host.includes(':')) return null // IP addresses
  const labels = host.split('.')
  // The last label is the top-level domain; never use it, unless it's all there is.
  const candidates = labels.length > 1 ? labels.slice(0, -1) : labels
  let label = candidates.find((l) => !generic.has(l.toLowerCase())) ?? candidates[0]
  label = label.replace(/[^a-zA-Z0-9_-]/g, '-').replace(/^-+/, '')
  if (!label) return null
  const name = unique(label, takenNames, (base, n) => `${base}${n}`)
  const key = unique(label.toUpperCase().replace(/[^A-Z0-9]/g, '_') + '_API_KEY', takenKeys, (base, n) =>
    base.replace(/_API_KEY$/, `_${n}_API_KEY`),
  )
  return { name, key }
}

function unique(value, taken, numbered) {
  let out = value
  for (let n = 2; taken.includes(out); n++) out = numbered(value, n)
  return out
}
