// A small client-side router: the current path as reactive state, and links that
// change it without reloading the page.

export const route = $state({ path: location.pathname, query: new URLSearchParams(location.search) })

export function navigate(to, { replace = false } = {}) {
  const url = new URL(to, location.origin)
  if (replace) history.replaceState(null, '', url)
  else history.pushState(null, '', url)
  route.path = url.pathname
  route.query = url.searchParams
  window.scrollTo(0, 0)
}

window.addEventListener('popstate', () => {
  route.path = location.pathname
  route.query = new URLSearchParams(location.search)
})

// Same-origin <a href> clicks become client-side navigation.
document.addEventListener('click', (e) => {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  const a = e.target.closest('a[href]')
  if (!a || a.target || a.hasAttribute('download')) return
  const url = new URL(a.href)
  if (url.origin !== location.origin || url.pathname === '/ca.crt' || url.pathname === '/setup') return
  // A link to a section of this page: let the browser scroll to it.
  if (url.hash && url.pathname === location.pathname && url.search === location.search) return
  e.preventDefault()
  navigate(url.pathname + url.search)
})
