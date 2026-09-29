// Who is signed in, shared by every page.

import { api } from './api.js'

export const session = $state({
  loaded: false,
  user: null,
  authMode: 'builtin',
  firstRun: false,
  version: '',
  switchyardVersion: '',
})

export async function loadSession() {
  const s = await api.get('/api/session')
  session.user = s.user
  session.authMode = s.auth_mode
  session.firstRun = !!s.first_run
  session.version = s.version
  session.switchyardVersion = s.switchyard_version
  session.loaded = true
}

export async function signOut() {
  await api.post('/api/logout').catch(() => {})
  session.user = null
}
