import { api } from '../api.js';
import { esc, icon, toast, panel, confirmDialog, loading, errorBox } from '../ui.js';

export async function renderDatabase(main, { db, dbName }) {
  const label = dbName || db;
  main.innerHTML = `
    <div class="page-head">
      <div class="crumbs">Collections <span class="sep">/</span> <span class="dim">${esc(label)}</span></div>
      <div class="head-actions">
        <button class="btn" id="apiPreview">${icon('external')} API preview</button>
        <button class="btn btn-primary" id="newCol">${icon('plus')} New collection</button>
        <button class="btn btn-danger" id="dropDb">${icon('trash')} Delete database</button>
      </div>
    </div>
    <div class="content" id="dbContent">${loading()}</div>`;
  const content = main.querySelector('#dbContent');

  async function load() {
    content.innerHTML = loading();
    try {
      const cols = await api(`/api/d/${db}/collections`);
      const base = cols.filter((c) => c.type !== 'auth').length;
      const auth = cols.filter((c) => c.type === 'auth').length;
      content.innerHTML = `
        <div class="stat-grid" style="margin-bottom:16px">
          <div class="stat"><div class="n">${cols.length}</div><div class="l">Collections</div></div>
          <div class="stat"><div class="n">${base}</div><div class="l">Base</div></div>
          <div class="stat"><div class="n">${auth}</div><div class="l">Auth</div></div>
        </div>
        ${cols.length ? `<div class="stat-grid">${cols.map((c) => `
          <a class="stat" href="#/d/${encodeURIComponent(db)}/c/${encodeURIComponent(c.name)}/records" style="text-decoration:none;color:inherit">
            <div style="display:flex;align-items:center;gap:10px;margin-bottom:8px">
              <span class="ico">${icon('collections', 18)}</span>
              <strong>${esc(c.name)}</strong>
              <span class="spacer"></span>
              <span class="badge ${c.type === 'auth' ? 'auth' : 'base'}">${esc(c.type)}</span>
            </div>
            <div class="l">${(c.fields || []).length} field(s)</div>
          </a>`).join('')}</div>`
          : `<div class="card"><div class="empty"><div class="big">🗂️</div>
              <p>No collections in <b>${esc(label)}</b> yet.</p>
              <button class="btn btn-primary" id="emptyNew">${icon('plus')} Create your first collection</button></div></div>`}`;
      const emptyNew = content.querySelector('#emptyNew');
      if (emptyNew) emptyNew.addEventListener('click', createCollection);
    } catch (e) {
      content.innerHTML = errorBox(e);
    }
  }

  function createCollection() {
    const { box, close } = panel({
      title: 'New collection',
      bodyHtml: `
        <div class="field"><label>Name</label><input id="col_name" placeholder="posts"></div>
        <div class="field"><label>Type</label>
          <select id="col_type">
            <option value="base">base</option>
            <option value="auth">auth (users can log in)</option>
          </select>
          <div class="hint">Auth collections automatically get email and password fields.</div>
        </div>`,
      footHtml: `<button class="btn" data-close>Cancel</button><button class="btn btn-primary" data-save>Create</button>`,
    });
    box.querySelector('[data-save]').addEventListener('click', async () => {
      const name = box.querySelector('#col_name').value.trim();
      const type = box.querySelector('#col_type').value;
      const fields = type === 'auth'
        ? [
            { name: 'email', type: 'email', required: true, unique: true },
            { name: 'password', type: 'text', required: true },
            { name: 'name', type: 'text' },
          ]
        : [];
      try {
        await api(`/api/d/${db}/collections`, { method: 'POST', body: JSON.stringify({ name, type, fields }) });
        close();
        toast('Collection created');
        location.hash = `#/d/${encodeURIComponent(db)}/c/${encodeURIComponent(name)}/schema`;
      } catch (e) { toast(e.message); }
    });
  }

  main.querySelector('#newCol').addEventListener('click', createCollection);
  main.querySelector('#apiPreview').addEventListener('click', () => {
    const base = `${location.origin}/api/d/${db}`;
    panel({
      title: 'API preview',
      bodyHtml: `<p class="muted">Base URLs for database <b>${esc(db)}</b>:</p>
        <pre class="help-box mono">GET    ${esc(base)}/collections
GET    ${esc(base)}/c/{collection}/records
POST   ${esc(base)}/c/{collection}/records
PATCH  ${esc(base)}/c/{collection}/records/{id}
DELETE ${esc(base)}/c/{collection}/records/{id}
POST   ${esc(base)}/c/{collection}/auth/login</pre>
        <p class="muted small">Send <code>Authorization: &lt;token&gt;</code>.</p>`,
    });
  });
  main.querySelector('#dropDb').addEventListener('click', async () => {
    if (!(await confirmDialog(`Delete database "${label}" with every collection, record and file?`, { okLabel: 'Delete database' }))) return;
    try {
      await api('/api/admin/databases/' + encodeURIComponent(db), { method: 'DELETE' });
      toast('Database deleted');
      localStorage.removeItem('mdb_db');
      location.hash = '#/';
      location.reload();
    } catch (e) { toast(e.message); }
  });

  await load();
}
