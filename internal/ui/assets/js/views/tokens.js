import { api } from '../api.js';
import { esc, icon, toast, panel, confirmDialog, loading, errorBox } from '../ui.js';

export async function renderTokens(main) {
  main.innerHTML = `
    <div class="page-head">
      <div class="crumbs">${icon('key', 18)} API tokens</div>
      <div class="head-actions">
        <button class="btn btn-primary" id="newToken">${icon('plus')} New token</button>
        <button class="btn btn-ghost btn-icon" id="reload" title="Reload">${icon('refresh')}</button>
      </div>
    </div>
    <div class="content" id="tokenContent">${loading()}</div>`;
  const content = main.querySelector('#tokenContent');
  let databases = [];

  async function load() {
    content.innerHTML = loading();
    try {
      const [tokens, dbs] = await Promise.all([
        api('/api/admin/tokens'),
        api('/api/admin/databases'),
      ]);
      databases = Array.isArray(dbs) ? dbs : [];
      const items = Array.isArray(tokens) ? tokens : [];
      content.innerHTML = `
        <div class="card">
          <h3>How tokens work</h3>
          <p class="card-sub">A token grants <b>full data access</b> (records, files, export/import) to <b>one database</b>.
            It cannot change schemas, settings, backups or run SQL. Send it as
            <code>Authorization: &lt;token&gt;</code> or <code>X-API-Key: &lt;token&gt;</code>.</p>
        </div>
        <div class="card">
          <h3>Active tokens</h3>
          ${items.length ? `<div class="table-wrap"><table class="grid">
            <thead><tr>
              <th class="no-sort">Name</th><th class="no-sort">Database</th><th class="no-sort">Prefix</th>
              <th class="no-sort">Created</th><th class="no-sort">Last used</th><th class="no-sort"></th>
            </tr></thead>
            <tbody>${items.map((t) => `
              <tr>
                <td>${esc(t.name)}</td>
                <td>${esc(t.dbName || t.dbId)}</td>
                <td class="mono small">${esc(t.prefix)}…</td>
                <td class="small">${esc(t.created)}</td>
                <td class="small">${esc(t.lastUsed || '—')}</td>
                <td class="actions">
                  <button class="btn btn-sm btn-danger" data-revoke="${esc(t.id)}" data-name="${esc(t.name)}">
                    ${icon('trash', 14)} Revoke
                  </button>
                </td>
              </tr>`).join('')}</tbody>
          </table></div>` : '<div class="empty"><div class="big">🔑</div>No API tokens yet.</div>'}
        </div>`;

      content.querySelectorAll('[data-revoke]').forEach((b) => b.addEventListener('click', async () => {
        if (!(await confirmDialog(`Revoke token "${b.dataset.name}"? Existing clients will stop working immediately.`, { okLabel: 'Revoke' }))) return;
        try {
          await api(`/api/admin/tokens/${encodeURIComponent(b.dataset.revoke)}`, { method: 'DELETE' });
          toast('Token revoked');
          load();
        } catch (e) { toast(e.message); }
      }));
    } catch (e) {
      content.innerHTML = errorBox(e);
    }
  }

  function showToken(raw) {
    const { box } = panel({
      title: 'Token created',
      bodyHtml: `<p class="muted">Copy this token now — it is shown only once.</p>
        <div class="field"><input id="rawToken" class="mono" readonly value="${esc(raw)}"></div>
        <div class="help-box">Use it as <code>Authorization: ${esc(raw.slice(0, 12))}…</code>
          or <code>X-API-Key</code>.</div>`,
      footHtml: `<button class="btn" data-close>Close</button>
        <button class="btn btn-primary" id="copyToken">${icon('file', 14)} Copy</button>`,
    });
    box.querySelector('#copyToken').addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(raw);
        toast('Token copied');
      } catch {
        box.querySelector('#rawToken').select();
        toast('Press Ctrl+C to copy');
      }
    });
  }

  main.querySelector('#newToken').addEventListener('click', () => {
    if (!databases.length) { toast('Create a database first'); return; }
    const { box, close } = panel({
      title: 'New API token',
      bodyHtml: `
        <div class="field"><label>Name</label><input id="tok_name" placeholder="Integrasi server A"></div>
        <div class="field"><label>Database</label>
          <select id="tok_db">${databases.map((d) => `<option value="${esc(d.id)}">${esc(d.name)}</option>`).join('')}</select>
        </div>`,
      footHtml: `<button class="btn" data-close>Cancel</button><button class="btn btn-primary" data-save>Create</button>`,
    });
    box.querySelector('[data-save]').addEventListener('click', async () => {
      const name = box.querySelector('#tok_name').value.trim();
      const dbId = box.querySelector('#tok_db').value;
      if (!name) { toast('Name is required'); return; }
      try {
        const res = await api('/api/admin/tokens', {
          method: 'POST', body: JSON.stringify({ name, dbId }),
        });
        close();
        showToken(res.token);
        load();
      } catch (e) { toast(e.message); }
    });
  });

  main.querySelector('#reload').addEventListener('click', load);
  await load();
}
