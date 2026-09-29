<script>
  // A panel with an optional header strip: the basic building block of every page.
  let { title = '', subtitle = '', actions, children, flush = false } = $props()
</script>

<section class="card">
  {#if title || actions}
    <header>
      <div>
        {#if title}<h2>{title}</h2>{/if}
        {#if subtitle}<p class="muted">{subtitle}</p>{/if}
      </div>
      {#if actions}<div class="actions">{@render actions()}</div>{/if}
    </header>
  {/if}
  <div class:body={!flush}>{@render children?.()}</div>
</section>

<style>
  .card {
    background: var(--card);
    border: 1px solid var(--line);
    border-radius: var(--radius);
    box-shadow: var(--shadow);
    min-width: 0;
  }
  header {
    display: flex; align-items: center; gap: 12px; justify-content: space-between;
    padding: 10px 16px; background: var(--card-head); border-bottom: 1px solid var(--line);
    border-radius: var(--radius) var(--radius) 0 0;
  }
  h2 { font-size: 14px; }
  header p { margin: 0; font-size: 12px; }
  .actions { display: flex; gap: 8px; flex-wrap: wrap; }
  .body { padding: 16px; }
</style>
