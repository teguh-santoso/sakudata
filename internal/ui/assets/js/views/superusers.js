import { api } from '../api.js';
import { esc, icon, toast, panel, confirmDialog, loading, errorBox } from '../ui.js';

export async function renderSuperusers(main) {
  main.innerHTML = `
    <div class="page-head">
      <div class="crumbs">Superusers</div>
      <div class="head-actions"><button class="btn btn-primary" id="add">${icon('plus')} New superuser</button></div>
    </div>
    <div class="content" id="suContent">${loading()}</div>`;
  const content = main.querySelector('#suContent');

  async function load() {
    content.innerHTML = loading();
    try {
      const items = await api('/api/admin/superusers');
      content.innerHTML = `
        <div class="table-wrap"><table class="grid">
          <thead><tr><th class="no-sort">Email</th><th class="no-sort">ID</th><th class="no-sort"></th></tr></thead>
          <tbody>${items.map((u) => `
            <tr>
              <td>${esc(u.email)}</td>
              <td class="mono small">${esc(u.id)}</td>
              <td class="actions">
                <button class="btn btn-sm" data-edit="${esc(u.id)}" data-email="${esc(u.email)}">${icon('edit', 14)} Edit</button>
                <button class="btn btn-sm btn-danger" data-del="${esc(u.id)}">${icon('trash', 14)}</button>
              </td>
            </tr>`).join('')}</tbody>
        </table></div>`;
      content.querySelectorAll('[data-edit]').forEach((b) => b.addEventListener('click', () => edit(b.dataset.edit, b.dataset.email)));
      content.querySelectorAll('[data-del]').forEach((b) => b.addEventListener('click', async () => {
        if (!(await confirmDialog('Delete this superuser?', { okLabel: 'Delete' }))) return;
        try { await api(`/api/admin/superusers/${b.dataset.del}`, { method: 'DELETE' }); toast('Superuser deleted'); load(); }
        catch (e) { toast(e.message); }
      }));
    } catch (e) {
      content.innerHTML = errorBox(e);
    }
  }

  function edit(id, email) {
    const { box, close } = panel({
      title: id ? 'Edit superuser' : 'New superuser',
      bodyHtml: `
        <div class="field"><label>Email</label><input id="su_email" type="email" value="${esc(email || '')}"></div>
        <div class="field"><label>Password ${id ? '<span class="muted">(leave empty to keep)</span>' : ''}</label>
          <input id="su_pass" type="password" autocomplete="new-password"></div>`,
      footHtml: `<button class="btn" data-close>Cancel</button><button class="btn btn-primary" data-save>Save</button>`,
    });
    box.querySelector('[data-save]').addEventListener('click', async () => {
      const em = box.querySelector('#su_email').value.trim();
      const pw = box.querySelector('#su_pass').value;
      try {
        if (id) await api(`/api/admin/superusers/${id}`, { method: 'PATCH', body: JSON.stringify({ email: em, password: pw }) });
        else await api('/api/admin/superusers', { method: 'POST', body: JSON.stringify({ email: em, password: pw }) });
        close();
        toast('Superuser saved');
        load();
      } catch (e) { toast(e.message); }
    });
  }

  main.querySelector('#add').addEventListener('click', () => edit('', ''));
  await load();
}
