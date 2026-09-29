<script>
  // A dialog. Closes on Escape or the backdrop unless `locked` (e.g. while saving).
  let { title, open = $bindable(false), locked = false, wide = false, children, footer, onclose } = $props()
  let dialog = $state()

  $effect(() => {
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    if (!open && dialog.open) dialog.close()
  })
</script>

<dialog
  class:wide
  bind:this={dialog}
  onclose={() => {
    open = false
    onclose?.()
  }}
  oncancel={(e) => locked && e.preventDefault()}
  onclick={(e) => e.target === dialog && !locked && (open = false)}
>
  {#if open}
    <div class="box">
      <header><h2>{title}</h2></header>
      <div class="body">{@render children?.()}</div>
      {#if footer}<footer>{@render footer()}</footer>{/if}
    </div>
  {/if}
</dialog>

<style>
  dialog {
    padding: 0; border: 1px solid var(--line-strong); border-radius: var(--radius);
    background: var(--card); color: var(--ink); width: min(560px, calc(100vw - 32px));
    box-shadow: 0 10px 40px #0008;
  }
  dialog.wide { width: min(860px, calc(100vw - 32px)); }
  dialog::backdrop { background: #0009; }
  .body { max-height: calc(100vh - 180px); overflow-y: auto; }
  /* Scroll the body instead of squashing what's in it. */
  .body > :global(*) { flex-shrink: 0; }
  header { padding: 12px 16px; background: var(--card-head); border-bottom: 1px solid var(--line); }
  h2 { font-size: 15px; }
  .body { padding: 16px; display: flex; flex-direction: column; gap: 14px; }
  footer { padding: 12px 16px; border-top: 1px solid var(--line); display: flex; gap: 8px; justify-content: flex-end; }
</style>
