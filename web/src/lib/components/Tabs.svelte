<script>
  let { tabs, active = $bindable() } = $props()
</script>

<div class="tabs" role="tablist">
  {#each tabs as [id, label]}
    <button role="tab" aria-selected={active === id} class:active={active === id} onclick={() => (active = id)}>{label}</button>
  {/each}
</div>

<style>
  .tabs { display: flex; gap: 2px; border-bottom: 1px solid var(--line); margin-bottom: 16px; overflow-x: auto; }
  button {
    background: none; border: none; border-bottom: 2px solid transparent; color: var(--ink-2);
    padding: 8px 14px; font: 600 13px var(--font); cursor: pointer; white-space: nowrap;
  }
  button:hover { color: var(--ink); }
  button.active { color: var(--ink); border-bottom-color: var(--accent); }
</style>
