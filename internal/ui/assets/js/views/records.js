import { api, upload } from '../api.js';
import { esc, icon, fmtValue, fmtDate, toast, panel, confirmDialog, loading, errorBox } from '../ui.js';

const SORTS = ['-created', 'created', 'id'];

function fieldWidget(f, value) {
  const id = `f_${f.name}`;
  const val = value ?? '';
  const required = f.required ? 'required' : '';
  switch (f.type) {
    case 'bool':
      return `<label class="field" style="display:flex;align-items:center;gap:9px">
        <input class="checkbox" type="checkbox" id="${attr(id)}" ${val ? 'checked' : ''}>
        <span>${esc(f.name)}</span></label>`;
    case 'json':
      return `<div class="field"><label for="${attr(id)}">${esc(f.name)}</label>
        <textarea class="code-input" id="${attr(id)}" rows="4">${esc(typeof val === 'object' ? JSON.stringify(val, null, 2) : val)}</textarea></div>`;
    case 'file':
      return `<div class="field"><label for="${attr(id)}">${esc(f.name)}</label>
        <input type="file" id="${attr(id)}">
        ${val ? `<div class="hint">Current: ${esc(val)}</div>` : ''}</div>`;
    case 'select':
      return `<div class="field"><label for="${attr(id)}">${esc(f.name)}</label>
        <select id="${attr(id)}" ${required}>
          <option value="">—</option>
          ${(f.options || []).map((o) => `<option ${o === val ? 'selected' : ''}>${esc(o)}</option>`).join('')}
        </select></div>`;
    case 'number':
      return `<div class="field"><label for="${attr(id)}">${esc(f.name)}</label>
        <input type="number" step="any" id="${attr(id)}" value="${esc(val)}" ${required}></div>`;
    case 'date':
      return `<div class="field"><label for="${attr(id)}">${esc(f.name)}</label>
        <input type="datetime-local" id="${attr(id)}" value="${esc(toLocalInput(val))}" ${required}></div>`;
    case 'email':
      return `<div class="field"><label for="${attr(id)}">${esc(f.name)}</label>
        <input type="email" id="${attr(id)}" value="${esc(val)}" ${required}></div>`;
    case 'url':
      return `<div class="field"><label for="${attr(id)}">${esc(f.name)}</label>
        <input type="url" id="${attr(id)}" value="${esc(val)}" ${required}></div>`;
    case 'password':
      return `<div class="field"><label for="${attr(id)}">${esc(f.name)}</label>
        <input type="password" id="${attr(id)}" value="" autocomplete="new-password" ${required}></div>`;
    default:
      return `<div class="field"><label for="${attr(id)}">${esc(f.name)}</label>
        <input id="${attr(id)}" value="${esc(val)}" ${required}></div>`;
  }
}

function attr(v) { return esc(v); }

function toLocalInput(v) {
  if (!v) return '';
  const d = new Date(v);
  if (isNaN(d.getTime())) return v;
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function collectForm(box, fields) {
  const data = {};
  for (const f of fields) {
    const el = box.querySelector(`#f_${CSS.escape(f.name)}`);
    if (!el) continue;
    if (f.type === 'file') continue;
    if (f.type === 'bool') { data[f.name] = el.checked; continue; }
    if (f.type === 'number') { if (el.value !== '') data[f.name] = Number(el.value); continue; }
    if (f.type === 'json') { if (el.value.trim() !== '') data[f.name] = el.value; continue; }
    if (f.type === 'password' && !el.value) continue;
    if (f.type === 'date' && el.value) { data[f.name] = new Date(el.value).toISOString(); continue; }
    data[f.name] = el.value;
  }
  if (fields.some((f) => f.name === 'email')) {
    const v = box.querySelector('#f_verified');
    if (v) data.verified = v.checked;
  }
  return data;
}

async function openRecordEditor({ db, collection, id, onSaved }) {
  let rec = {};
  if (id) rec = await api(`/api/d/${db}/c/${collection.name}/records/${id}`);
  const fields = collection.fields || [];
  const body = fields
    .filter((f) => !(f.name === 'password' && id))
    .map((f) => fieldWidget(f, rec[f.name]))
    .join('') +
    (fields.some((f) => f.name === 'email')
      ? `<div class="field"><label style="display:flex;gap:9px;align-items:center"><input class="checkbox" type="checkbox" id="f_verified" ${rec.verified ? 'checked' : ''}> verified</label></div>`
      : '');
  const foot = `<button class="btn" data-close>Cancel</button>
    <button class="btn btn-primary" data-save>${id ? 'Save changes' : 'Create record'}</button>`;
  const { box, close } = panel({
    title: id ? `Edit ${collection.name}` : `New ${collection.name}`,
    bodyHtml: body || '<p class="muted">This collection has no fields.</p>',
    footHtml: foot,
    drawer: true,
  });
  box.querySelector('[data-save]').addEventListener('click', async () => {
    const btn = box.querySelector('[data-save]');
    btn.disabled = true;
    try {
      const data = collectForm(box, fields);
      let saved;
      if (id) saved = await api(`/api/d/${db}/c/${collection.name}/records/${id}`, { method: 'PATCH', body: JSON.stringify(data) });
      else saved = await api(`/api/d/${db}/c/${collection.name}/records`, { method: 'POST', body: JSON.stringify(data) });
      for (const f of fields) {
        if (f.type !== 'file') continue;
        const el = box.querySelector(`#f_${CSS.escape(f.name)}`);
        if (el && el.files.length) {
          await upload(`/api/d/${db}/c/${collection.name}/records/${saved.id}/files/${encodeURIComponent(f.name)}`, el.files[0]);
        }
      }
      close();
      toast(id ? 'Record updated' : 'Record created');
      onSaved();
    } catch (e) {
      toast(e.message);
      btn.disabled = false;
    }
  });
}

export async function renderRecords(container, { db, collection }) {
  const state = { page: 1, perPage: 20, sort: '-created', filter: '', q: '' };
  const fields = collection.fields || [];

  container.innerHTML = `
    <div class="toolbar">
      <div class="searchbar">${icon('search')}
        <input id="recFilter" placeholder='Filter expression, e.g. name ~ "budi" && active = true'>
      </div>
      <select id="recPer" class="input" style="width:auto">
        <option value="10">10 / page</option>
        <option value="20" selected>20 / page</option>
        <option value="50">50 / page</option>
        <option value="100">100 / page</option>
      </select>
      <button class="btn" id="expJson">${icon('file')} Export</button>
      <button class="btn" id="impBtn">${icon('up')} Import</button>
      <input type="file" id="impFile" accept=".json,application/json" hidden>
      <button class="btn btn-primary" id="newRec">${icon('plus')} New record</button>
      <button class="btn btn-ghost btn-icon" id="reload" title="Reload">${icon('refresh')}</button>
    </div>
    <div class="content" id="recContent">${loading()}</div>`;

  const content = container.querySelector('#recContent');
  const toolbar = container.querySelector('.toolbar');
  // toolbar lives inside the scrollable area's sibling; normalize spacing
  toolbar.classList.add('page-head');

  async function load() {
    content.innerHTML = loading();
    try {
      const qs = new URLSearchParams({ page: state.page, perPage: state.perPage, sort: state.sort });
      if (state.filter) qs.set('filter', state.filter);
      const res = await api(`/api/d/${db}/c/${collection.name}/records?` + qs);
      renderTable(res);
    } catch (e) {
      content.innerHTML = errorBox(e);
    }
  }

  function sortIndicator(name) {
    if (state.sort === `-${name}`) return ' ▾';
    if (state.sort === name) return ' ▴';
    return '';
  }

  function renderTable(res) {
    const items = res.items || [];
    const cols = ['id', ...fields.map((f) => f.name), 'created'];
    if (!items.length) {
      content.innerHTML = `<div class="card"><div class="empty"><div class="big">🗂️</div>
        <p>No records yet.</p>
        <button class="btn btn-primary" id="emptyNew">${icon('plus')} New record</button></div></div>`;
      content.querySelector('#emptyNew').addEventListener('click', () => openRecordEditor({ db, collection, onSaved: load }));
      return;
    }
    const head = cols.map((c) => {
      const isId = c === 'id';
      return `<th data-sort="${esc(c)}" class="${isId ? '' : ''}">${esc(c)}${sortIndicator(c)}</th>`;
    }).join('');
    const rows = items.map((r) => `
      <tr>
        <td><input type="checkbox" class="rowsel" value="${esc(r.id)}"></td>
        ${cols.map((c) => {
          const f = fields.find((x) => x.name === c);
          let cell;
          if (c === 'id') {
            cell = `<span class="idpill">${esc(r.id)}</span>`;
          } else if (f && f.type === 'file' && r[c]) {
            cell = `<a href="#" class="file-link" data-dl="${esc(r.id)}" data-field="${esc(c)}" data-name="${esc(r[c])}">${esc(r[c])}</a>`;
          } else if (c === 'created' || c === 'updated') {
            cell = esc(fmtDate(r[c]));
          } else {
            cell = fmtValue(r[c], f && f.type);
          }
          return `<td>${cell}</td>`;
        }).join('')}
        <td class="actions">
          <button class="btn btn-sm" data-edit="${esc(r.id)}">${icon('edit', 14)} Edit</button>
          <button class="btn btn-sm btn-danger" data-del="${esc(r.id)}">${icon('trash', 14)}</button>
        </td>
      </tr>`).join('');

    content.innerHTML = `
      <div class="table-wrap">
        <table class="grid">
          <thead><tr><th class="no-sort"><input type="checkbox" id="selAll"></th>${head}<th class="no-sort"></th></tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
      <div class="pager">
        <button class="btn btn-sm" id="prev" ${res.page <= 1 ? 'disabled' : ''}>‹ Prev</button>
        <span>Page ${res.page} of ${res.totalPages || 1} · ${res.totalItems} record(s)</span>
        <button class="btn btn-sm" id="next" ${res.page >= res.totalPages ? 'disabled' : ''}>Next ›</button>
        <span class="spacer"></span>
        <button class="btn btn-sm btn-danger" id="delSel">${icon('trash', 14)} Delete selected</button>
      </div>
      <p class="muted small" style="margin-top:8px">Click a column header to sort.</p>`;

    content.querySelectorAll('th[data-sort]').forEach((th) => {
      th.addEventListener('click', () => {
        const col = th.dataset.sort;
        state.sort = state.sort === col ? `-${col}` : state.sort === `-${col}` ? col : `-${col}`;
        state.page = 1;
        load();
      });
    });
    content.querySelector('#prev').addEventListener('click', () => { state.page--; load(); });
    content.querySelector('#next').addEventListener('click', () => { state.page++; load(); });
    content.querySelector('#selAll').addEventListener('change', (e) => {
      content.querySelectorAll('.rowsel').forEach((c) => { c.checked = e.target.checked; });
    });
    content.querySelectorAll('[data-edit]').forEach((b) => b.addEventListener('click', () => {
      openRecordEditor({ db, collection, id: b.dataset.edit, onSaved: load });
    }));
    content.querySelectorAll('[data-del]').forEach((b) => b.addEventListener('click', async () => {
      if (!(await confirmDialog('Delete this record?', { okLabel: 'Delete' }))) return;
      try {
        await api(`/api/d/${db}/c/${collection.name}/records/${b.dataset.del}`, { method: 'DELETE' });
        toast('Record deleted');
        load();
      } catch (e) { toast(e.message); }
    }));
    content.querySelectorAll('[data-dl]').forEach((a) => a.addEventListener('click', async (e) => {
      e.preventDefault();
      const res2 = await fetch(`/api/d/${db}/c/${collection.name}/records/${a.dataset.dl}/files/${a.dataset.field}/${encodeURIComponent(a.dataset.name)}`, {
        headers: { Authorization: localStorage.getItem('mdb_token') || '' },
      });
      if (!res2.ok) { toast('Download failed'); return; }
      const blob = await res2.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url; link.download = a.dataset.name; link.click();
      setTimeout(() => URL.revokeObjectURL(url), 4000);
    }));
    content.querySelector('#delSel').addEventListener('click', async () => {
      const ids = [...content.querySelectorAll('.rowsel:checked')].map((c) => c.value);
      if (!ids.length) { toast('Nothing selected'); return; }
      if (!(await confirmDialog(`Delete ${ids.length} record(s)?`, { okLabel: 'Delete' }))) return;
      for (const id of ids) {
        try { await api(`/api/d/${db}/c/${collection.name}/records/${id}`, { method: 'DELETE' }); } catch (e) { toast(e.message); }
      }
      toast('Records deleted');
      load();
    });
  }

  toolbar.querySelector('#newRec').addEventListener('click', () => openRecordEditor({ db, collection, onSaved: load }));
  toolbar.querySelector('#reload').addEventListener('click', load);
  toolbar.querySelector('#expJson').addEventListener('click', async () => {
    const qs = new URLSearchParams({ format: 'json', sort: state.sort });
    if (state.filter) qs.set('filter', state.filter);
    const res = await fetch(`/api/d/${db}/c/${collection.name}/export?` + qs, {
      headers: { Authorization: localStorage.getItem('mdb_token') || '' },
    });
    if (!res.ok) { toast('Export failed'); return; }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url; a.download = `${collection.name}.json`; a.click();
    setTimeout(() => URL.revokeObjectURL(url), 5000);
  });
  const impFile = toolbar.querySelector('#impFile');
  toolbar.querySelector('#impBtn').addEventListener('click', () => impFile.click());
  impFile.addEventListener('change', async () => {
    const file = impFile.files[0];
    if (!file) return;
    try {
      const text = await file.text();
      const rows = JSON.parse(text);
      if (!Array.isArray(rows)) throw new Error('JSON must be an array of records');
      const result = await api(`/api/d/${db}/c/${collection.name}/import`, {
        method: 'POST', body: JSON.stringify(rows),
      });
      toast(`Imported ${result.created}, failed ${result.failed}`);
      load();
    } catch (e) {
      toast(e.message);
    } finally {
      impFile.value = '';
    }
  });
  toolbar.querySelector('#recPer').addEventListener('change', (e) => {
    state.perPage = Number(e.target.value); state.page = 1; load();
  });
  const filterInput = toolbar.querySelector('#recFilter');
  filterInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { state.filter = filterInput.value.trim(); state.page = 1; load(); }
  });

  await load();
}
