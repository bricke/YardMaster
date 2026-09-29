<script>
  // YardMaster's mark and name. public/favicon.svg is the same mark.
  let { size = 22 } = $props()
</script>

<span class="logo">
  <svg viewBox="0 0 32 32" width={size} height={size} aria-hidden="true"><rect width="32" height="32" rx="6" fill="var(--accent)" /><path d="M6 24 L16 8 L26 24 M10 18 H22 M16 8 V24" stroke="#fff" stroke-width="2.4" fill="none" stroke-linecap="round" stroke-linejoin="round" /></svg>
  <span>YardMaster</span>
</span>

<style>
  .logo { display: flex; align-items: center; gap: 10px; font-weight: 700; }
</style>
