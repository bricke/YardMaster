// Every call to YardMaster's /api goes through here, so errors are handled one way.

export class ApiError extends Error {
  constructor(status, message, body) {
    super(message)
    this.status = status
    this.body = body
  }
}

async function request(method, path, body) {
  const opts = { method, headers: {}, credentials: 'same-origin' }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  let res
  try {
    res = await fetch(path, opts)
  } catch {
    throw new ApiError(0, "YardMaster isn't reachable. Check your connection and try again.")
  }
  let data = null
  const text = await res.text()
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = { error: text }
    }
  }
  if (!res.ok) {
    if (res.status === 401 && path !== '/api/login' && onSignedOut) onSignedOut()
    throw new ApiError(res.status, data?.error || `Request failed (${res.status})`, data)
  }
  return data
}

let onSignedOut = null
/** Called when the session has ended, so the app can show the login screen. */
export function whenSignedOut(fn) {
  onSignedOut = fn
}

export const api = {
  get: (path) => request('GET', path),
  post: (path, body = {}) => request('POST', path, body),
  put: (path, body = {}) => request('PUT', path, body),
  del: (path) => request('DELETE', path),
}
