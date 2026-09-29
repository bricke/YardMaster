<script>
  // A read-only value with a copy button. The button only appears where the browser
  // allows copying (HTTPS or localhost), so it never pretends to copy.
  let { value, secret = false } = $props()
  let copied = $state(false)
  const canCopy = typeof navigator !== 'undefined' && !!navigator.clipboard && window.isSecureContext

  async function copy() {
    await navigator.clipboard.writeText(value)
    copied = true
    setTimeout(() => (copied = false), 1500)
  }
</script>

<div class="copy">
  <code class:secret>{value}</code>
  {#if canCopy}<button class="btn small" type="button" onclick={copy}>{copied ? 'Copied' : 'Copy'}</button>{/if}
</div>

<style>
  .copy { display: flex; gap: 8px; align-items: center; }
  code {
    flex: 1; min-width: 0; padding: 7px 10px; background: var(--input); border: 1px solid var(--line-strong);
    border-radius: var(--radius); overflow-x: auto; white-space: nowrap; user-select: all;
  }
  code.secret { border-color: var(--warning); }
</style>
