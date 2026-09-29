<script>
  // Time-series chart (uPlot). One y-axis only; two measures of different scale get two
  // charts. Hovering shows a crosshair and every series' value in the legend.
  //
  //   x:      Unix seconds
  //   series: [{ label, values, color: 'series-1' }]
  //   kind:   'line' or 'bars'
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'

  let { x, series, kind = 'line', height = 200, format = (v) => v, label = 'Chart' } = $props()
  let el = $state()
  let width = $state(0)
  let themeTick = $state(0)

  const css = (name) => getComputedStyle(document.documentElement).getPropertyValue('--' + name).trim()

  // Redraw when the theme changes (OS setting or the toggle).
  $effect(() => {
    const bump = () => themeTick++
    const mq = matchMedia('(prefers-color-scheme: light)')
    mq.addEventListener('change', bump)
    const mo = new MutationObserver(bump)
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    return () => {
      mq.removeEventListener('change', bump)
      mo.disconnect()
    }
  })

  $effect(() => {
    if (!el || !width) return
    themeTick
    const axis = { stroke: css('muted'), grid: { stroke: css('grid'), width: 1 }, ticks: { stroke: css('axis'), width: 1 } }
    const bars = kind === 'bars' ? uPlot.paths.bars({ size: [0.6, 48], radius: 0.15 }) : undefined
    const opts = {
      width,
      height,
      cursor: { points: { size: 8 }, drag: { x: false, y: false } },
      legend: { live: true },
      scales: { x: { time: true }, y: { range: (u, min, max) => [0, max > 0 ? max * 1.1 : 1] } },
      axes: [
        {
          ...axis,
          // One short label per tick, far enough apart not to collide: a 24-hour time for
          // live charts, a date for daily ones.
          space: 72,
          values: (u, vals) =>
            vals.map((v) =>
              new Date(v * 1000).toLocaleString(
                undefined,
                kind === 'bars' ? { month: 'short', day: 'numeric' } : { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' },
              ),
            ),
        },
        { ...axis, values: (u, vals) => vals.map((v) => format(v)), size: 60 },
      ],
      series: [
        {
          label: kind === 'bars' ? 'Day' : 'Time',
          value: (u, v) =>
            v == null ? '—' : new Date(v * 1000).toLocaleString(undefined, kind === 'bars' ? { dateStyle: 'medium' } : { timeStyle: 'short' }),
        },
        ...series.map((s) => ({
          label: s.label,
          stroke: css(s.color || 'series-1'),
          width: kind === 'bars' ? 0 : 2,
          fill: kind === 'bars' ? css(s.color || 'series-1') : css(s.color || 'series-1') + '22',
          paths: bars,
          points: { show: false },
          value: (u, v) => (v === null || v === undefined ? '—' : format(v)),
        })),
      ],
    }
    const data = [x, ...series.map((s) => s.values)]
    const plot = new uPlot(opts, data, el)
    return () => plot.destroy()
  })
</script>

<div class="chart" bind:clientWidth={width} role="img" aria-label={label}>
  <div bind:this={el}></div>
</div>

<style>
  .chart { width: 100%; min-width: 0; }
  .chart :global(.u-legend) { text-align: left; }
  .chart :global(.u-legend th) { font-weight: 600; }
</style>
