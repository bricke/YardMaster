<script>
  // Date range presets for usage views.
  let { days = $bindable(30) } = $props()
  const options = [
    [1, 'Today'],
    [7, 'Last 7 days'],
    [30, 'Last 30 days'],
    [90, 'Last 90 days'],
  ]
</script>

<select bind:value={days} aria-label="Date range" style="width:auto">
  {#each options as [d, label]}<option value={d}>{label}</option>{/each}
</select>
