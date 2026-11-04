export class ApiError extends Error {
  constructor(message, status, data) {
    super(message);
    this.status = status;
    this.data = data;
  }
}

export const state = {
  token: localStorage.getItem('mdb_token') || '',
  meta: { appName: 'SakuData', version: '', fieldTypes: [] },
  theme: localStorage.getItem('mdb_theme') || 'light',
  db: localStorage.getItem('mdb_dbid') || localStorage.getItem('mdb_db') || '',
  dbName: '',
};

export function setToken(token) {
  state.token = token || '';
  if (token) localStorage.setItem('mdb_token', token);
  else localStorage.removeItem('mdb_token');
}

export function setDb(id) {
  state.db = id || '';
  if (id) localStorage.setItem('mdb_dbid', id);
  else {
    localStorage.removeItem('mdb_dbid');
    localStorage.removeItem('mdb_db');
  }
}

export async function api(path, opts = {}) {
  const headers = Object.assign({}, opts.headers || {});
  if (opts.body !== undefined && !(opts.body instanceof FormData)) {
    headers['Content-Type'] = 'application/json';
  }
  if (state.token) headers['Authorization'] = state.token;
  const res = await fetch(path, Object.assign({}, opts, { headers }));
  if (res.status === 204) return null;
  const ct = res.headers.get('content-type') || '';
  const data = ct.includes('application/json') ? await res.json().catch(() => null) : null;
  if (!res.ok) {
    if (res.status === 401 && state.token) {
      window.dispatchEvent(new CustomEvent('mdb:unauth'));
    }
    throw new ApiError((data && data.message) || `Request failed (${res.status})`, res.status, data);
  }
  return data;
}

export async function loadMeta() {
  try {
    state.meta = await api('/api/meta');
  } catch {
    /* keep defaults */
  }
  return state.meta;
}

export async function upload(path, file) {
  const fd = new FormData();
  fd.append('file', file);
  return api(path, { method: 'POST', body: fd });
}
