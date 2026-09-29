<script>
  // Router state as icon + label + colour, never colour alone.
  let { status } = $props()
  const look = {
    up: ['good', '●', 'Router up'],
    restarting: ['warning', '↻', 'Router restarting'],
    starting: ['warning', '↻', 'Router starting'],
    down: ['critical', '✕', 'Router down'],
    stopped: ['critical', '✕', 'Router stopped'],
    unconfigured: ['warning', '○', 'Not configured'],
  }
  const [kind, icon, label] = $derived(look[status] || ['', '?', 'Router status unknown'])
</script>

<span class="chip {kind}" title="Status of the LLM router"><span aria-hidden="true">{icon}</span>{label}</span>
