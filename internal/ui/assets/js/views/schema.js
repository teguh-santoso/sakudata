import { api, state } from '../api.js';
import { esc, icon, toast, confirmDialog } from '../ui.js';

const SYSTEM_FIELDS = ['id', 'created', 'updated'];

export function renderSchema(container, { db, collection, reload }) {
  let fields = (collection.fields || []).map((f) => Object.assign({}, f));
  const types = state.meta.fieldTypes && state.meta.fieldTypes.length
    ? state.meta.fieldTypes
    : ['text', 'number', 'bool', 'json', 'file', 'email', 'url', 'date', 'select', 'relation', 'autodate'];

  container.innerHTML = `
    <div class="content">
      <div class="card">
        <h3>Fields</h3>
        <p class="card-sub">Add, reorder or remove fields. Existing rows are preserved when possible.</p>
        <div class="field-head">
          <span>Name</span><span>Type</span><span>Required</span><span>Unique</span><span></span>
        </div>
        <div id="fieldRows"></div>
        <div style="margin-top:12px;display:flex;gap:8px">
          <button class="btn" id="addField">${icon('plus')} Add field</button>
          <button class="btn btn-primary" id="saveSchema">${icon('check')} Save schema</button>
        </div>
      </div>
      <div class="card">
        <h3>System fields</h3>
        <p class="card-sub">These columns are managed automatically and cannot be edited.</p>
        <div class="select-options">${SYSTEM_FIELDS.map((f) => `<span class="chip mono">${f}</span>`).join('')}</div>
      </div>
      <div class="card" style="border-color:var(--danger)">
        <h3 style="color:var(--danger)">Danger zone</h3>
        <p class="card-sub">Deleting a collection removes its table, records and stored files.</p>
        <button class="btn btn-danger" id="dropCol">${icon('trash')} Delete collection</button>
      </div>
    </div>`;

  const rows = container.querySelector('#fieldRows');

  function draw() {
    rows.innerHTML = fields.map((f, i) => `
      <div class="field-row" data-i="${i}">
        <input data-k="name" value="${esc(f.name)}" placeholder="field_name">
        <select data-k="type">
          ${types.map((t) => `<option ${t === f.type ? 'selected' : ''}>${esc(t)}</option>`).join('')}
        </select>
        <label style="text-align:center"><input class="checkbox" type="checkbox" data-k="required" ${f.required ? 'checked' : ''}></label>
        <label style="text-align:center"><input class="checkbox" type="checkbox" data-k="unique" ${f.unique ? 'checked' : ''}></label>
        <div class="move">
          <button class="btn btn-ghost btn-sm" data-up="${i}" title="Move up">${icon('up', 13)}</button>
          <button class="btn btn-ghost btn-sm" data-remove="${i}" title="Remove">${icon('trash', 13)}</button>
        </div>
      </div>
      ${f.type === 'select' ? `<div class="field-row" data-opts="${i}" style="grid-template-columns:1fr">
        <input data-opt="${i}" value="${esc((f.options || []).join(', '))}" placeholder="Options, comma separated: draft, published">
      </div>` : ''}`).join('');

    rows.querySelectorAll('.field-row[data-i]').forEach((row) => {
      const i = Number(row.dataset.i);
      row.querySelectorAll('[data-k]').forEach((el) => {
        const handler = () => {
          const k = el.dataset.k;
          fields[i][k] = el.type === 'checkbox' ? el.checked : el.value;
          if (k === 'type') draw();
        };
        el.addEventListener(el.tagName === 'SELECT' ? 'change' : 'input', handler);
      });
    });
    rows.querySelectorAll('[data-up]').forEach((b) => b.addEventListener('click', () => {
      const i = Number(b.dataset.up);
      if (i > 0) { [fields[i - 1], fields[i]] = [fields[i], fields[i - 1]]; draw(); }
    }));
    rows.querySelectorAll('[data-remove]').forEach((b) => b.addEventListener('click', async () => {
      const i = Number(b.dataset.remove);
      if (!(await confirmDialog(`Remove field "${fields[i].name}"? Existing data for this field will be dropped on save.`, { okLabel: 'Remove' }))) return;
      fields.splice(i, 1);
      draw();
    }));
    rows.querySelectorAll('[data-opt]').forEach((el) => el.addEventListener('input', () => {
      const i = Number(el.dataset.opt);
      fields[i].options = el.value.split(',').map((s) => s.trim()).filter(Boolean);
    }));
  }

  draw();

  container.querySelector('#addField').addEventListener('click', () => {
    fields.push({ name: `field${fields.length + 1}`, type: 'text' });
    draw();
  });

  container.querySelector('#saveSchema').addEventListener('click', async () => {
    const cleaned = fields.map((f) => ({
      name: (f.name || '').trim(),
      type: f.type,
      required: !!f.required,
      unique: !!f.unique,
      ...(f.type === 'select' ? { options: f.options || [] } : {}),
    }));
    if (cleaned.some((f) => !f.name)) { toast('Field names cannot be empty'); return; }
    try {
      await api(`/api/d/${db}/collections/${collection.name}`, {
        method: 'PATCH', body: JSON.stringify({ fields: cleaned }),
      });
      toast('Schema saved');
      reload();
    } catch (e) {
      toast(e.message);
    }
  });

  container.querySelector('#dropCol').addEventListener('click', async () => {
    if (!(await confirmDialog(`Delete collection "${collection.name}" and all of its records?`, { okLabel: 'Delete' }))) return;
    try {
      await api(`/api/d/${db}/collections/${collection.name}`, { method: 'DELETE' });
      toast('Collection deleted');
      location.hash = `#/d/${encodeURIComponent(db)}`;
    } catch (e) { toast(e.message); }
  });
}
