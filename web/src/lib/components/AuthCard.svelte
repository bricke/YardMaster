<script>
  // Centered card for sign-in style screens.
  import Logo from './Logo.svelte'
  let { title, subtitle = '', children } = $props()
</script>

<div class="wrap">
  <div class="card">
    <div class="brand"><Logo size={30} /></div>
    <h1>{title}</h1>
    {#if subtitle}<p class="muted">{subtitle}</p>{/if}
    {@render children?.()}
  </div>
</div>

<style>
  .wrap { min-height: 100vh; display: flex; flex-direction: column; align-items: center; justify-content: center; padding: 16px; }
  .card {
    width: min(400px, 100%); background: var(--card); border: 1px solid var(--line); border-radius: 8px;
    padding: 28px; box-shadow: 0 8px 30px #0004; display: flex; flex-direction: column; gap: 14px;
  }
  .brand { font-size: 18px; }
  h1 { font-size: 18px; }
  p { margin: -8px 0 0; }
</style>
