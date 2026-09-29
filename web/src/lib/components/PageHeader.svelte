<script>
  let { title, subtitle = '', actions } = $props()
</script>

<div class="head">
  <div>
    <h1>{title}</h1>
    {#if subtitle}<p class="muted">{subtitle}</p>{/if}
  </div>
  {#if actions}<div class="row">{@render actions()}</div>{/if}
</div>

<style>
  .head { display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; flex-wrap: wrap; margin-bottom: 20px; }
  h1 { font-size: 20px; }
  p { margin: 2px 0 0; }
</style>
