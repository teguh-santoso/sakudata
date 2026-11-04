import { api, state, setToken, setDb, loadMeta } from './api.js';
import { applyTheme, esc, icon, brandMark, toast, panel, confirmDialog } from './ui.js';
import { renderLogin } from './views/login.js';
import { renderRecords } from './views/records.js';
import { renderSchema } from './views/schema.js';
import { renderRules } from './views/rules.js';
import { renderSettings } from './views/settings.js';
import { renderLogs } from './views/logs.js';
import { renderBackups } from './views/backups.js';
import { renderSQL } from './views/sql.js';
import { renderTokens } from './views/tokens.js';
import { renderSuperusers } from './views/superusers.js';
import { renderDatabase } from './views/databases.js';

const root = document.getElementById('root');
let collections = [];
let search = '';
let mainEl = null;

applyTheme(state.theme);

function decodeEmail(token) {
  try {
    const payload = JSON.parse(atob(token.split('.')[1].replaceAll('-', '+').replaceAll('_', '/')));
    return payload.email || '';
  } catch { return ''; }
}

function logout() {
  setToken('');
  setDb('');
  location.hash = '';
  showLogin();
}

function showLogin() {
  renderLogin(root, boot);
}

async function boot() {
  await loadMeta();
  applyTheme(state.theme);
  if (!state.token) return showLogin();
  try {
    await api('/api/admin/databases');
  } catch {
    setToken('');
    return showLogin();
  }
  renderShell();
  await refresh();
}

function renderShell() {
  const name = state.meta.appName || 'SakuData';
  const email = decodeEmail(state.token);
  root.innerHTML = `
    <div class="shell">
      <div class="topbar">
        <button class="btn btn-ghost btn-icon" id="sidebarBtn" title="Toggle sidebar">${icon('window')}</button>
        <div class="brand"><span class="mark">${brandMark(18)}</span><span class="appname">${esc(name)}</span></div>
        <button class="tab" data-nav="collections" title="Collections" aria-label="Collections">${icon('collections')} <span class="lbl">Collections</span></button>
        <button class="tab" data-nav="sql" title="SQL console" aria-label="SQL console">${icon('terminal')} <span class="lbl">SQL</span></button>
        <button class="tab" data-nav="settings" title="Settings" aria-label="Settings">${icon('settings')} <span class="lbl">Settings</span></button>
        <button class="tab" data-nav="logs" title="Logs" aria-label="Logs">${icon('logs')} <span class="lbl">Logs</span></button>
        <button class="tab" data-nav="backups" title="Backups" aria-label="Backups">${icon('database')} <span class="lbl">Backups</span></button>
        <button class="tab" data-nav="tokens" title="API tokens" aria-label="API tokens">${icon('key')} <span class="lbl">API</span></button>
        <button class="tab" data-nav="superusers" title="Superusers" aria-label="Superusers">${icon('users')} <span class="lbl">Superusers</span></button>
        <span class="spacer"></span>
        <span class="who">${esc(email || 'superuser')}</span>
        <button class="btn btn-ghost btn-icon" id="themeBtn" title="Toggle theme">${icon(state.theme === 'dark' ? 'sun' : 'moon')}</button>
        <button class="btn btn-ghost btn-icon" id="logoutBtn" title="Logout">${icon('logout')}</button>
      </div>
      <div class="body">
        <div class="sidebar-backdrop" id="sidebarBackdrop"></div>
        <aside class="sidebar">
          <div class="side-head"><select id="dbSelect" title="Switch database"></select></div>
          <div class="side-search">${icon('search')}<input id="colSearch" placeholder="Search collections..." value="${esc(search)}"></div>
          <div class="nav" id="sideNav"></div>
          <div class="side-foot">
            <button class="btn" id="newDb">${icon('plus')} <span>New database</span></button>
          </div>
        </aside>
        <main class="main" id="main"></main>
      </div>
    </div>`;
  const shell = root.querySelector('.shell');
  if (localStorage.getItem('mdb_sidebar') === 'collapsed') shell.classList.add('sidebar-collapsed');
  root.querySelector('#sidebarBtn').addEventListener('click', () => {
    if (window.matchMedia('(max-width: 860px)').matches) {
      shell.classList.toggle('sidebar-open');
      return;
    }
    shell.classList.toggle('sidebar-collapsed');
    localStorage.setItem('mdb_sidebar', shell.classList.contains('sidebar-collapsed') ? 'collapsed' : 'open');
  });
  root.querySelector('#sidebarBackdrop').addEventListener('click', () => shell.classList.remove('sidebar-open'));
  root.querySelector('#sideNav').addEventListener('click', (e) => {
    if (e.target.closest('a.item') && window.matchMedia('(max-width: 860px)').matches) {
      shell.classList.remove('sidebar-open');
    }
  });
  window.addEventListener('resize', () => {
    if (!window.matchMedia('(max-width: 860px)').matches) shell.classList.remove('sidebar-open');
  });
  mainEl = root.querySelector('#main');

  root.querySelector('#themeBtn').addEventListener('click', () => {
    applyTheme(state.theme === 'dark' ? 'light' : 'dark');
    root.querySelector('#themeBtn').innerHTML = icon(state.theme === 'dark' ? 'sun' : 'moon');
  });
  root.querySelector('#logoutBtn').addEventListener('click', logout);

  root.querySelectorAll('[data-nav]').forEach((btn) => btn.addEventListener('click', () => {
    const nav = btn.dataset.nav;
    if (nav === 'collections') location.hash = state.db ? `#/d/${state.db}` : '#/';
    else location.hash = `#/${nav}`;
  }));
  root.querySelector('#dbSelect').addEventListener('change', (e) => {
    setDb(e.target.value);
    location.hash = `#/d/${encodeURIComponent(e.target.value)}`;
  });
  root.querySelector('#colSearch').addEventListener('input', (e) => {
    search = e.target.value;
    renderNav();
  });
  root.querySelector('#newDb').addEventListener('click', createDatabase);
}

async function createDatabase() {
  const { box, close } = panel({
    title: 'New database',
    bodyHtml: `<div class="field"><label>Name</label><input id="db_name" placeholder="my_project">
      <div class="hint">Letters, digits and underscore only.</div></div>`,
    footHtml: `<button class="btn" data-close>Cancel</button><button class="btn btn-primary" data-save>Create</button>`,
  });
  box.querySelector('[data-save]').addEventListener('click', async () => {
    const name = box.querySelector('#db_name').value.trim();
    try {
      const d = await api('/api/admin/databases', { method: 'POST', body: JSON.stringify({ name }) });
      close();
      setDb(d.id);
      state.dbName = d.name;
      toast('Database created');
      location.hash = `#/d/${encodeURIComponent(d.id)}`;
      await refresh();
    } catch (e) { toast(e.message); }
  });
}

async function loadDbs() {
  try {
    const dbs = await api('/api/admin/databases');
    state.dbs = Array.isArray(dbs) ? dbs : [];
    const ids = state.dbs.map((d) => d.id);
    if (!state.db || !ids.includes(state.db)) setDb(ids[0] || '');
    const current = state.dbs.find((d) => d.id === state.db);
    state.dbName = current ? current.name : '';
  } catch (e) {
    state.dbs = [];
    state.dbName = '';
  }
  const sel = root.querySelector('#dbSelect');
  if (sel) {
    sel.innerHTML = state.dbs.length
      ? state.dbs.map((d) => `<option value="${esc(d.id)}" ${d.id === state.db ? 'selected' : ''}>${esc(d.name)}</option>`).join('')
      : '<option value="">No databases</option>';
  }
}

async function loadSidebar() {
  collections = [];
  if (state.db) {
    try {
      const res = await api(`/api/d/${state.db}/collections`);
      collections = Array.isArray(res) ? res : [];
    } catch { collections = []; }
  }
  renderNav();
}

function renderNav() {
  const nav = root.querySelector('#sideNav');
  if (!nav) return;
  const q = search.toLowerCase();
  const list = collections.filter((c) => c.name.toLowerCase().includes(q));
  const hash = location.hash;
  nav.innerHTML = `
    <div class="sec">Collections</div>
    ${list.length ? list.map((c) => {
      const active = hash.includes(`/c/${encodeURIComponent(c.name)}/`);
      return `<a class="item ${active ? 'active' : ''}" href="#/d/${encodeURIComponent(state.db)}/c/${encodeURIComponent(c.name)}/records">
        <span class="ico">${icon('collections', 15)}</span><span class="lbl">${esc(c.name)}</span>
        <span class="tag">${esc(c.type === 'auth' ? 'auth' : 'base')}</span></a>`;
    }).join('') : `<div class="muted small" style="padding:8px">${state.db ? 'No collections yet' : 'Create a database first'}</div>`}`;
}

function setActiveNav(nav) {
  root.querySelectorAll('[data-nav]').forEach((b) => b.classList.toggle('active', b.dataset.nav === nav));
}

function route() {
  if (!mainEl) return;
  const hash = location.hash.slice(1) || '/';
  const parts = hash.split('/').filter(Boolean);

  if (parts[0] === 'sql') { setActiveNav('sql'); return renderSQL(mainEl, { db: state.db, dbName: state.dbName }); }
  if (parts[0] === 'settings') { setActiveNav('settings'); return renderSettings(mainEl); }
  if (parts[0] === 'logs') { setActiveNav('logs'); return renderLogs(mainEl); }
  if (parts[0] === 'backups') { setActiveNav('backups'); return renderBackups(mainEl); }
  if (parts[0] === 'tokens') { setActiveNav('tokens'); return renderTokens(mainEl); }
  if (parts[0] === 'superusers') { setActiveNav('superusers'); return renderSuperusers(mainEl); }

  setActiveNav('collections');
  if (parts[0] === 'd' && parts[1]) {
    let db = decodeURIComponent(parts[1]);
    let rec = (state.dbs || []).find((d) => d.id === db);
    if (!rec) {
      // Upgrade a legacy name URL to the opaque ID without an extra reload.
      const byName = (state.dbs || []).find((d) => d.name === db);
      if (byName) {
        db = byName.id;
        rec = byName;
        const suffix = parts.length > 2 ? '/' + parts.slice(2).join('/') : '';
        history.replaceState(null, '', `#/d/${encodeURIComponent(db)}${suffix}`);
      }
    }
    setDb(db);
    state.dbName = rec ? rec.name : db;
    if (parts[2] === 'c' && parts[3]) {
      return renderCollection(mainEl, db, decodeURIComponent(parts[3]), parts[4] || 'records');
    }
    return renderDatabase(mainEl, { db, dbName: state.dbName });
  }
  if (state.db) {
    location.hash = `#/d/${encodeURIComponent(state.db)}`;
    return;
  }
  mainEl.innerHTML = `<div class="content"><div class="card"><div class="empty">
    <div class="big">🗂️</div><p>No databases yet.</p>
    <button class="btn btn-primary" id="emptyDb">${icon('plus')} Create database</button></div></div></div>`;
  mainEl.querySelector('#emptyDb').addEventListener('click', createDatabase);
}

async function renderCollection(main, db, name, tab) {
  main.innerHTML = `<div class="content"><div class="empty"><div class="big">⏳</div>Loading…</div></div>`;
  let collection;
  try {
    collection = await api(`/api/d/${db}/collections/${encodeURIComponent(name)}`);
  } catch (e) {
    main.innerHTML = `<div class="content"><div class="card"><div class="empty">
      <div class="big">⚠️</div><p>${esc(e.message)}</p>
      <a class="btn" href="#/d/${encodeURIComponent(db)}">Back to collections</a></div></div></div>`;
    return;
  }
  const isAuth = collection.type === 'auth';
  const tabs = [['records', 'Records'], ['schema', 'Schema'], ['rules', 'API rules']];
  if (isAuth) tabs.push(['auth', 'Auth']);

  main.innerHTML = `
    <div class="page-head">
      <div class="crumbs">${icon('collections', 18)}
        <a href="#/d/${encodeURIComponent(db)}">${esc(state.dbName || db)}</a>
        <span class="sep">/</span><span>${esc(collection.name)}</span>
        <span class="badge ${isAuth ? 'auth' : 'base'}">${esc(collection.type)}</span>
      </div>
      <div class="head-actions">
        <button class="btn" id="apiBtn">${icon('external')} API</button>
        <button class="btn btn-ghost btn-icon" id="reloadCol" title="Reload">${icon('refresh')}</button>
      </div>
    </div>
    <div class="tabs">
      ${tabs.map(([key, label]) => `<button class="tab ${tab === key ? 'active' : ''}" data-tab="${key}">${label}</button>`).join('')}
    </div>
    <div id="tabContent" style="flex:1;min-height:0;display:flex;flex-direction:column"></div>
    <div class="footer"><span>${esc(collection.name)}</span><span class="spacer"></span><span>${(collection.fields || []).length} field(s)</span></div>`;

  main.querySelectorAll('[data-tab]').forEach((b) => b.addEventListener('click', () => {
    location.hash = `#/d/${encodeURIComponent(db)}/c/${encodeURIComponent(collection.name)}/${b.dataset.tab}`;
  }));
  main.querySelector('#reloadCol').addEventListener('click', () => renderCollection(main, db, name, tab));
  main.querySelector('#apiBtn').addEventListener('click', () => {
    const base = `${location.origin}/api/d/${db}/c/${collection.name}/records`;
    panel({
      title: 'API preview',
      bodyHtml: `<pre class="help-box mono">GET    ${esc(base)}?page=1&amp;perPage=20&amp;sort=-created&amp;filter=name ~ "budi"
POST   ${esc(base)}
GET    ${esc(base)}/{id}
PATCH  ${esc(base)}/{id}
DELETE ${esc(base)}/{id}
POST   ${esc(base)}/../auth/login

# curl
curl "${esc(base)}?page=1" \\
  -H "Authorization: $TOKEN"

curl -X POST "${esc(base)}" \\
  -H "Authorization: $TOKEN" -H "Content-Type: application/json" \\
  -d '{"title": "hello"}'</pre>`,
    });
  });

  const content = main.querySelector('#tabContent');
  const reload = () => renderCollection(main, db, name, tab);
  if (tab === 'schema') renderSchema(content, { db, collection, reload });
  else if (tab === 'rules' || tab === 'auth') renderRules(content, { db, collection, reload });
  else renderRecords(content, { db, collection });
}

async function refresh() {
  await loadDbs();
  await loadSidebar();
  route();
}

window.addEventListener('hashchange', () => {
  if (!root.querySelector('.shell')) return;
  loadSidebar().then(route);
});
window.addEventListener('mdb:unauth', () => {
  toast('Session expired, please sign in again');
  logout();
});

boot();
