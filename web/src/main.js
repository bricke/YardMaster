import './theme.css'
import { mount } from 'svelte'
import App from './App.svelte'

// Apply the saved theme choice before the first paint.
try {
  const t = localStorage.getItem('ym-theme')
  if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t
} catch {}

mount(App, { target: document.getElementById('app') })
