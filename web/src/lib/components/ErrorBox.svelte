<script>
  // Shows an error message, or nothing. Accepts a string or an Error.
  let { error } = $props()
  const text = $derived(typeof error === 'string' ? error : error?.message)
</script>

{#if text}
  <div class="alert critical" role="alert"><strong>⚠ </strong>{text}</div>
{/if}
