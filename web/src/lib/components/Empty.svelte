<script>
  let { children } = $props()
</script>

<div class="empty muted">{@render children?.()}</div>

<style>
  .empty { padding: 28px 16px; text-align: center; }
</style>
