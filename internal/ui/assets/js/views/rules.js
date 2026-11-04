import { api } from '../api.js';
import { esc, icon, toast, panel } from '../ui.js';

const OPS = ['list', 'view', 'create', 'update', 'delete'];
const MODES = [
  ['', 'Superusers only'],
  ['*', 'Public (anyone)'],
  ['user', 'Any logged-in user'],
  ['custom', 'Custom rule…'],
];

function modeOf(value) {
  if (!value) return '';
  if (value === '*') return '*';
  if (value === 'user') return 'user';
  return 'custom';
}

function fieldNames(collection) {
  return ['id', 'created', 'updated', ...(collection.fields || [])
    .map((f) => f.name)
    .filter((name) => name !== 'password')];
}

function quoteValue(raw) {
  const v = (raw || '').trim();
  if (v === '') return '""';
  if (/^-?\d+(\.\d+)?$/.test(v) || v === 'true' || v === 'false' || v === 'null') return v;
  return JSON.stringify(v);
}

function openBuilder(op, current, collection, apply) {
  const fields = fieldNames(collection);
  const conditions = [];

  const body = `
    <p class="muted small" style="margin-top:0">Add one or more conditions. All conditions are combined with the selected joiner.</p>
    <div id="condRows"></div>
    <button class="btn btn-sm" id="addCond">${icon('plus')} Add condition</button>
    <div class="field" style="margin-top:16px">
      <label>Join conditions with</label>
      <select id="join"> <option value="&&">AND</option><option value="||">OR</option></select>
    </div>
    <div class="help-box"><b>Preview:</b><div id="preview" class="mono" style="margin-top:6px">—</div></div>`;

  const { box, close } = panel({
    title: `Rule builder · ${op}`,
    bodyHtml: body,
    footHtml: `<button class="btn" data-close>Cancel</button>
      <button class="btn btn-primary" id="applyExpr">Use this rule</button>`,
  });

  const rows = box.querySelector('#condRows');
  const join = box.querySelector('#join');
  const preview = box.querySelector('#preview');

  function updatePreview() {
    const parts = conditions
      .filter((c) => c.field && c.op)
      .map((c) => {
        let rhs;
        if (c.source === 'auth-id') rhs = '@request.auth.id';
        else if (c.source === 'auth-email') rhs = '@request.auth.email';
        else if (c.source === 'auth-col') rhs = '@request.auth.collectionName';
        else rhs = quoteValue(c.value);
        return `${c.field} ${c.op} ${rhs}`;
      });
    preview.textContent = parts.length ? parts.join(` ${join.value} `) : '—';
    return parts.join(` ${join.value} `);
  }

  function draw() {
    rows.innerHTML = conditions.map((c, i) => `
      <div class="field-row" data-c="${i}" style="grid-template-columns:1fr 90px 1.2fr 90px 34px">
        <select data-k="field" data-i="${i}">
          ${fields.map((f) => `<option ${f === c.field ? 'selected' : ''}>${esc(f)}</option>`).join('')}
        </select>
        <select data-k="op" data-i="${i}">
          ${['=', '!=', '>', '>=', '<', '<=', '~', '!~'].map((o) => `<option ${o === c.op ? 'selected' : ''}>${esc(o)}</option>`).join('')}
        </select>
        ${c.source === 'value'
          ? `<input data-k="value" data-i="${i}" value="${esc(c.value)}" placeholder="value">`
          : `<select data-k="source" data-i="${i}">
               <option value="auth-id" ${c.source === 'auth-id' ? 'selected' : ''}>@request.auth.id</option>
               <option value="auth-email" ${c.source === 'auth-email' ? 'selected' : ''}>@request.auth.email</option>
               <option value="auth-col" ${c.source === 'auth-col' ? 'selected' : ''}>@request.auth.collectionName</option>
             </select>`}
        <select data-k="sourceType" data-i="${i}">
          <option value="value" ${c.source === 'value' ? 'selected' : ''}>literal</option>
          <option value="auth-id" ${c.source !== 'value' ? 'selected' : ''}>auth field</option>
        </select>
        <button class="btn btn-ghost btn-sm" data-rm="${i}" title="Remove">${icon('trash', 13)}</button>
      </div>`).join('');

    rows.querySelectorAll('[data-k]').forEach((el) => {
      el.addEventListener(el.tagName === 'SELECT' ? 'change' : 'input', () => {
        const i = Number(el.dataset.i);
        const k = el.dataset.k;
        if (k === 'sourceType') {
          conditions[i].source = el.value;
          draw();
          return;
        }
        if (k === 'source') {
          conditions[i].source = el.value;
        } else {
          conditions[i][k] = el.value;
        }
        updatePreview();
      });
    });
    rows.querySelectorAll('[data-rm]').forEach((b) => b.addEventListener('click', () => {
      conditions.splice(Number(b.dataset.rm), 1);
      draw();
      updatePreview();
    }));
    updatePreview();
  }

  box.querySelector('#addCond').addEventListener('click', () => {
    conditions.push({ field: fields.includes('owner') ? 'owner' : fields[0], op: '=', source: 'value', value: '' });
    draw();
  });
  join.addEventListener('change', updatePreview);
  box.querySelector('#applyExpr').addEventListener('click', () => {
    const expr = updatePreview();
    apply(expr);
    close();
  });

  if (current && !['', '*', 'user'].includes(current)) {
    // Seed a single row from a simple `field op value` expression when possible.
    const m = current.match(/^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(=|!=|>=|<=|>|<|~|!~)\s*(.+)\s*$/);
    if (m) {
      let source = 'value';
      let value = m[3].trim();
      if (value === '@request.auth.id') source = 'auth-id';
      else if (value === '@request.auth.email') source = 'auth-email';
      else if (value === '@request.auth.collectionName') source = 'auth-col';
      else value = value.replace(/^["']|["']$/g, '');
      conditions.push({ field: m[1], op: m[2], source, value });
    }
  }
  if (!conditions.length) conditions.push({ field: fields[0], op: '=', source: 'value', value: '' });
  draw();
}

export function renderRules(container, { db, collection, reload }) {
  const rules = collection.rules || {};
  const hooks = collection.hooks || {};
  const options = collection.options || {};
  const hookLines = Object.entries(hooks)
    .flatMap(([action, urls]) => (urls || []).map((u) => `${action}: ${u}`))
    .join('\n');

  container.innerHTML = `
    <div class="content">
      <div class="card">
        <h3>API rules</h3>
        <p class="card-sub">Choose who can perform each operation. Pick a preset, or build a custom rule with the visual builder.</p>
        <div id="ruleRows">
          ${OPS.map((op) => {
            const value = rules[op] || '';
            const mode = modeOf(value);
            return `<div class="setting-row" data-op="${op}">
              <div style="width:110px;font-weight:600;text-transform:capitalize">${op}</div>
              <select class="rule-mode" data-op="${op}" style="width:190px">
                ${MODES.map(([v, label]) => `<option value="${v}" ${v === mode ? 'selected' : ''}>${esc(label)}</option>`).join('')}
              </select>
              <input class="code-input rule-expr ${mode === 'custom' ? '' : 'hidden'}" data-op="${op}"
                value="${esc(mode === 'custom' ? value : '')}" placeholder='field = @request.auth.id'>
              <button class="btn btn-sm rule-build ${mode === 'custom' ? '' : 'hidden'}" data-op="${op}">${icon('settings', 14)} Builder</button>
              <span class="spacer"></span>
            </div>`;
          }).join('')}
        </div>
        <div class="help-box" style="margin-top:14px">
          <b>Examples:</b>
          <button class="btn btn-sm" data-preset="owner">Only owner</button>
          <button class="btn btn-sm" data-preset="public-read">Public read, superuser write</button>
          <button class="btn btn-sm" data-preset="auth-create">Logged-in users can create</button>
          <div style="margin-top:8px">Tokens: <code>@request.auth.id</code>, <code>@request.auth.email</code>,
            <code>@request.auth.collectionName</code>. Operators: <code>= != &gt; &gt;= &lt; &lt;= ~ !~ &amp;&amp; || !</code>.</div>
        </div>
      </div>

      <div class="card">
        <h3>Webhooks</h3>
        <p class="card-sub">One per line, format <code>action: https://example.com/hook</code> (actions: create, update, delete).</p>
        <textarea id="hooks" class="code-input" rows="4" placeholder="create: https://example.com/hook">${esc(hookLines)}</textarea>
      </div>

      ${collection.type === 'auth' ? `
      <div class="card">
        <h3>Auth options</h3>
        <div class="field">
          <label for="opt_min">Minimum password length</label>
          <input type="number" id="opt_min" min="6" max="72" value="${esc(options.minPasswordLength || 8)}">
        </div>
        <label class="field" style="display:flex;gap:9px;align-items:center">
          <input class="checkbox" type="checkbox" id="opt_verify" ${options.requireVerified ? 'checked' : ''}>
          <span>Require verified email to log in</span>
        </label>
      </div>` : ''}

      <div style="display:flex;justify-content:flex-end">
        <button class="btn btn-primary" id="saveRules">${icon('check')} Save</button>
      </div>
    </div>`;

  function rowEl(op) { return container.querySelector(`.setting-row[data-op="${op}"]`); }
  function exprFor(op) {
    const row = rowEl(op);
    const mode = row.querySelector('.rule-mode').value;
    if (mode !== 'custom') return mode;
    return row.querySelector('.rule-expr').value.trim();
  }
  function setMode(op, mode, value) {
    const row = rowEl(op);
    row.querySelector('.rule-mode').value = mode;
    const input = row.querySelector('.rule-expr');
    const build = row.querySelector('.rule-build');
    const custom = mode === 'custom';
    input.classList.toggle('hidden', !custom);
    build.classList.toggle('hidden', !custom);
    if (custom) input.value = value ?? input.value;
    else input.value = '';
  }

  container.querySelectorAll('.rule-mode').forEach((sel) => {
    sel.addEventListener('change', () => setMode(sel.dataset.op, sel.value, ''));
  });
  container.querySelectorAll('.rule-build').forEach((btn) => {
    btn.addEventListener('click', () => {
      const op = btn.dataset.op;
      openBuilder(op, rowEl(op).querySelector('.rule-expr').value, collection, (expr) => {
        rowEl(op).querySelector('.rule-expr').value = expr;
      });
    });
  });

  container.querySelectorAll('[data-preset]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const preset = btn.dataset.preset;
      if (preset === 'owner') {
        for (const op of ['view', 'update', 'delete']) {
          setMode(op, 'custom', 'owner = @request.auth.id');
        }
      } else if (preset === 'public-read') {
        setMode('list', '*'); setMode('view', '*');
        for (const op of ['create', 'update', 'delete']) setMode(op, '');
      } else if (preset === 'auth-create') {
        setMode('create', 'custom', '@request.auth.id != ""');
      }
      toast('Preset applied');
    });
  });

  container.querySelector('#saveRules').addEventListener('click', async () => {
    const body = {
      rules: {
        list: exprFor('list'),
        view: exprFor('view'),
        create: exprFor('create'),
        update: exprFor('update'),
        delete: exprFor('delete'),
      },
      hooks: {},
    };
    for (const line of container.querySelector('#hooks').value.split('\n')) {
      const t = line.trim();
      if (!t) continue;
      const i = t.indexOf(':');
      if (i < 0) continue;
      const action = t.slice(0, i).trim();
      const url = t.slice(i + 1).trim();
      if (!['create', 'update', 'delete'].includes(action) || !url) continue;
      (body.hooks[action] = body.hooks[action] || []).push(url);
    }
    if (collection.type === 'auth') {
      body.options = {
        ...options,
        minPasswordLength: Number(container.querySelector('#opt_min').value) || 8,
        requireVerified: container.querySelector('#opt_verify').checked,
      };
    }
    try {
      await api(`/api/d/${db}/collections/${collection.name}`, { method: 'PATCH', body: JSON.stringify(body) });
      toast('Rules saved');
      reload();
    } catch (e) { toast(e.message); }
  });
}
