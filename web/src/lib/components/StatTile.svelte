<script>
  // A headline number with its label. `note` explains the number in plain words.
  // Content passed as children (a status badge, say) takes the number's place.
  let { label, value = '', note = '', children } = $props()
</script>

<div class="tile">
  <div class="label">{label}</div>
  {#if children}<div class="content">{@render children()}</div>{:else}<div class="value">{value}</div>{/if}
  {#if note}<div class="note muted">{note}</div>{/if}
</div>

<style>
  .tile {
    background: var(--card); border: 1px solid var(--line); border-radius: var(--radius);
    padding: 14px 16px; box-shadow: var(--shadow); min-width: 0;
  }
  .label { font-size: 12px; font-weight: 600; color: var(--ink-2); text-transform: uppercase; letter-spacing: 0.03em; }
  .value { font-size: 26px; font-weight: 650; margin-top: 4px; overflow-wrap: anywhere; }
  .content { margin: 8px 0; }
  .note { font-size: 12px; margin-top: 2px; }
</style>
