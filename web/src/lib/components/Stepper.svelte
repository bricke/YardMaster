<script>
  // Numbered steps for the setup wizard. Every step can be revisited.
  let { steps, current, onselect } = $props()
</script>

<ol class="stepper">
  {#each steps as label, i}
    <li class:done={i < current} class:active={i === current}>
      <button onclick={() => onselect(i)} aria-current={i === current ? 'step' : undefined}>
        <span class="n">{i < current ? '✓' : i + 1}</span>{label}
      </button>
    </li>
  {/each}
</ol>

<style>
  .stepper { list-style: none; display: flex; gap: 8px; padding: 0; margin: 0 0 16px; flex-wrap: wrap; }
  li { flex: 1; min-width: 140px; }
  button {
    width: 100%; display: flex; align-items: center; gap: 10px; padding: 10px 12px;
    background: var(--card); border: 1px solid var(--line); border-radius: var(--radius);
    color: var(--ink-2); font: 600 13px var(--font); cursor: pointer; text-align: left;
  }
  button:hover { border-color: var(--line-strong); }
  .active button { border-color: var(--accent); color: var(--ink); box-shadow: inset 0 -2px 0 var(--accent); }
  .n {
    width: 24px; height: 24px; border-radius: 50%; display: grid; place-items: center; flex: none;
    background: var(--raised); border: 1px solid var(--line-strong); font-size: 12px;
  }
  .active .n { background: var(--accent); border-color: var(--accent); color: #fff; }
  .done .n { color: var(--good); border-color: var(--good); }
</style>
