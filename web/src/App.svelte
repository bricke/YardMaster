<script>
  import { onMount } from 'svelte'
  import { session, loadSession, signOut } from './lib/session.svelte.js'
  import { route, navigate } from './lib/router.svelte.js'
  import { api, whenSignedOut } from './lib/api.js'
  import StatusBadge from './lib/components/StatusBadge.svelte'
  import ErrorBox from './lib/components/ErrorBox.svelte'
  import Logo from './lib/components/Logo.svelte'

  import Login from './pages/Login.svelte'
  import FirstRun from './pages/FirstRun.svelte'
  import ChangePassword from './pages/ChangePassword.svelte'
  import MyTokens from './pages/MyTokens.svelte'
  import Connect from './pages/Connect.svelte'
  import Account from './pages/Account.svelte'
  import NotFound from './pages/NotFound.svelte'
  import Help from './pages/Help.svelte'
  import Dashboard from './pages/admin/Dashboard.svelte'
  import Usage from './pages/admin/Usage.svelte'
  import Users from './pages/admin/Users.svelte'
  import Config from './pages/admin/Config.svelte'
  import Router from './pages/admin/Router.svelte'
  import Playground from './pages/admin/Playground.svelte'
  import Prices from './pages/admin/Prices.svelte'
  import Audit from './pages/admin/Audit.svelte'
  import Settings from './pages/admin/Settings.svelte'

  // The one table of pages: path, page, who can open it, sidebar label and group.
  const pages = [
    { path: '/', page: MyTokens, role: 'user', label: 'Tokens & usage', group: 'Me' },
    { path: '/', page: Dashboard, role: 'admin', label: 'Dashboard', group: 'Monitor' },
    { path: '/usage', page: Usage, role: 'admin', label: 'Usage', group: 'Monitor' },
    { path: '/router', page: Router, role: 'admin', label: 'Router health', group: 'Monitor' },
    { path: '/config', page: Config, role: 'admin', label: 'Setup', group: 'Configure' },
    { path: '/prices', page: Prices, role: 'admin', label: 'Prices', group: 'Configure' },
    { path: '/playground', page: Playground, role: 'admin', label: 'Playground', group: 'Configure' },
    { path: '/users', page: Users, role: 'admin', label: 'Users', group: 'Manage' },
    { path: '/audit', page: Audit, role: 'admin', label: 'Audit log', group: 'Manage' },
    { path: '/settings', page: Settings, role: 'admin', label: 'Settings', group: 'Manage' },
    { path: '/tokens', page: MyTokens, role: 'admin', label: 'My tokens', group: 'Me' },
    { path: '/connect', page: Connect, role: 'any', label: 'Connect an agent', group: 'Me' },
    { path: '/account', page: Account, role: 'any', label: 'Account', group: 'Me' },
    { path: '/help/routing', page: Help, role: 'any', label: 'How routing works', group: 'Help' },
  ]

  const role = $derived(session.user?.role)
  const visible = $derived(pages.filter((p) => p.role === 'any' || p.role === role))
  const current = $derived(visible.find((p) => p.path === route.path))
  const groups = $derived([...new Set(visible.map((p) => p.group))])

  let status = $state('')
  let loadError = $state('')
  let menuOpen = $state(false)
  let navOpen = $state(false)

  onMount(async () => {
    whenSignedOut(() => (session.user = null))
    try {
      await loadSession()
    } catch (e) {
      loadError = e.message
    }
  })

  // The router badge every signed-in person sees.
  $effect(() => {
    if (!session.user || session.user.must_change_password) return
    let stop = false
    const poll = async () => {
      try {
        status = (await api.get('/api/status')).state
      } catch {}
      if (!stop) timer = setTimeout(poll, 10000)
    }
    let timer
    poll()
    return () => {
      stop = true
      clearTimeout(timer)
    }
  })

  $effect(() => {
    route.path
    navOpen = false
    menuOpen = false
  })

  async function logout() {
    await signOut()
    navigate('/', { replace: true })
  }

  function toggleTheme() {
    const root = document.documentElement
    const dark = root.dataset.theme
      ? root.dataset.theme === 'dark'
      : !matchMedia('(prefers-color-scheme: light)').matches
    root.dataset.theme = dark ? 'light' : 'dark'
    try {
      localStorage.setItem('ym-theme', root.dataset.theme)
    } catch {}
  }
</script>

{#if !session.loaded}
  <div class="center">{#if loadError}<ErrorBox error={loadError} />{:else}<span class="muted">Loading…</span>{/if}</div>
{:else if !session.user && session.firstRun}
  <FirstRun />
{:else if !session.user}
  <Login />
{:else if session.user.must_change_password}
  <ChangePassword forced />
{:else}
  <div class="shell">
    <header class="topbar">
      <button class="hamburger" aria-label="Menu" onclick={() => (navOpen = !navOpen)}>☰</button>
      <a class="brand" href="/"><Logo /></a>
      <span class="spacer"></span>
      {#if status}<StatusBadge status={status} />{/if}
      <button class="icon" onclick={toggleTheme} title="Switch light or dark theme" aria-label="Switch theme"><svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="2" /><path d="M12 4a8 8 0 0 1 0 16z" fill="currentColor" /></svg></button>
      <div class="menu">
        <button class="user" onclick={() => (menuOpen = !menuOpen)} aria-expanded={menuOpen}>
          {session.user.display_name} <span class="muted">▾</span>
        </button>
        {#if menuOpen}
          <div class="dropdown">
            <div class="muted small">{session.user.username} · {session.user.role}</div>
            <a href="/account">Account</a>
            {#if session.authMode === 'builtin'}<button onclick={logout}>Sign out</button>{/if}
          </div>
        {/if}
      </div>
    </header>

    <nav class="sidebar" class:open={navOpen}>
      {#each groups as group}
        <div class="group">{group}</div>
        {#each visible.filter((p) => p.group === group) as p}
          <a href={p.path} class:active={p === current}>{p.label}</a>
        {/each}
      {/each}
      <div class="foot muted">
        <div>YardMaster {session.version}</div>
        <div>Switchyard {session.switchyardVersion}</div>
      </div>
    </nav>

    <main>
      {#if current}
        {#key current}<current.page />{/key}
      {:else}
        <NotFound />
      {/if}
    </main>
  </div>
{/if}

<style>
  .center { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
  .shell { min-height: 100vh; }
  .topbar {
    position: fixed; inset: 0 0 auto 0; height: var(--topbar-h); z-index: 20;
    display: flex; align-items: center; gap: 12px; padding: 0 16px;
    background: var(--topbar); color: #e6edf3; border-bottom: 1px solid #ffffff14;
  }
  .brand { color: #fff; font-size: 16px; }
  .brand:hover { text-decoration: none; }
  .icon, .hamburger, .user {
    display: inline-flex; align-items: center;
    background: none; border: 1px solid transparent; color: inherit; font: inherit; cursor: pointer;
    padding: 6px 8px; border-radius: var(--radius);
  }
  .icon:hover, .hamburger:hover, .user:hover { border-color: #ffffff30; }
  .hamburger { display: none; font-size: 18px; }
  .menu { position: relative; }
  .dropdown {
    position: absolute; right: 0; top: calc(100% + 6px); min-width: 200px; z-index: 30;
    background: var(--card); color: var(--ink); border: 1px solid var(--line-strong);
    border-radius: var(--radius); box-shadow: 0 8px 24px #0006; padding: 6px;
    display: flex; flex-direction: column;
  }
  .dropdown > * { padding: 7px 10px; border-radius: 4px; text-align: left; }
  .dropdown a, .dropdown button { color: var(--ink); background: none; border: none; font: inherit; cursor: pointer; }
  .dropdown a:hover, .dropdown button:hover { background: var(--accent-soft); text-decoration: none; }

  .sidebar {
    position: fixed; top: var(--topbar-h); bottom: 0; left: 0; width: var(--sidebar-w); z-index: 10;
    background: var(--sidebar); padding: 12px 8px; overflow-y: auto; display: flex; flex-direction: column;
    border-right: 1px solid #ffffff10;
  }
  .group {
    font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em;
    color: #8b949e; padding: 14px 12px 6px;
  }
  .sidebar a {
    display: block; padding: 8px 12px; border-radius: var(--radius); color: var(--sidebar-ink);
    border-left: 3px solid transparent; font-weight: 500;
  }
  .sidebar a:hover { background: #ffffff0d; text-decoration: none; }
  .sidebar a.active { background: var(--sidebar-active); border-left-color: var(--accent); color: #fff; }
  .foot { margin-top: auto; padding: 16px 12px 4px; font-size: 11px; line-height: 1.6; color: #6e7781; }

  main { margin-left: var(--sidebar-w); padding: calc(var(--topbar-h) + 24px) 28px 40px; max-width: 1400px; }

  @media (max-width: 860px) {
    .hamburger { display: block; }
    .sidebar { transform: translateX(-100%); transition: transform 0.15s; }
    .sidebar.open { transform: none; box-shadow: 0 0 40px #000a; }
    main { margin-left: 0; padding: calc(var(--topbar-h) + 16px) 16px 32px; }
    .user { max-width: 40vw; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  }
</style>
