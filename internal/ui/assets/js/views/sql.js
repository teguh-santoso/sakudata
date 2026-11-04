import { api } from '../api.js';
import { esc, icon, toast, confirmDialog } from '../ui.js';

const DESTRUCTIVE = /^\s*(drop|truncate|alter|delete|update|insert|replace|vacuum|attach|detach|create)\b/i;

function renderValue(v) {
  if (v === null || v === undefined) return '<span class="sql-null">NULL</span>';
  if (typeof v === 'object') return `<code class="mono">${esc(JSON.stringify(v))}</code>`;
  return esc(v);
}

export async function renderSQL(main, { db, dbName }) {
  if (!db) {
    main.innerHTML = `<div class="page-head"><div class="crumbs">SQL</div></div>
      <div class="content"><div class="card"><div class="empty"><div class="big">🗄️</div>Select a database first.</div></div></div>`;
    return;
  }
  main.innerHTML = `
    <div class="page-head">
      <div class="crumbs">${icon('terminal', 18)} SQL <span class="sep">/</span>
        <span class="dim">${esc(dbName || db)}</span></div>
      <div class="head-actions">
        <button class="btn btn-primary" id="runSql">${icon('terminal')} Run <span class="small muted">Ctrl+Enter</span></button>
      </div>
    </div>
    <div class="content">
      <div class="sql-layout">
        <div class="card">
          <h3>Tables</h3>
          <div class="sql-tables" id="sqlTables"><span class="muted small">Loading…</span></div>
        </div>
        <div>
          <div class="card">
            <div class="sql-toolbar">
              <button class="btn btn-sm" id="presetTables">List tables</button>
              <button class="btn btn-sm" id="presetDescribe">Describe table</button>
              <button class="btn btn-sm" id="presetCount">Count rows</button>
              <span class="spacer"></span>
              <span class="muted small">One statement per run. Results are capped at 1000 rows.</span>
            </div>
            <textarea id="sqlQuery" class="code-input" rows="7" spellcheck="false"
              placeholder="SELECT * FROM &quot;collection&quot; LIMIT 20;">SELECT 1;</textarea>
          </div>
          <div class="card" id="sqlResultCard" style="display:none">
            <h3 id="sqlResultTitle">Result</h3>
            <div id="sqlResult"></div>
          </div>
        </div>
      </div>
    </div>`;

  const queryEl = main.querySelector('#sqlQuery');
  const resultCard = main.querySelector('#sqlResultCard');
  const resultEl = main.querySelector('#sqlResult');
  const tablesEl = main.querySelector('#sqlTables');
  const titleEl = main.querySelector('#sqlResultTitle');
  let firstTable = '';

  async function exec(query) {
    const res = await api(`/api/d/${db}/sql`, {
      method: 'POST',
      body: JSON.stringify({ query }),
    });
    return res;
  }

  async function loadTables() {
    try {
      const res = await exec(`SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`);
      const rows = res.rows || [];
      if (!rows.length) {
        tablesEl.innerHTML = '<span class="muted small">No tables.</span>';
        return;
      }
      firstTable = rows[0][0];
      queryEl.value = `SELECT * FROM "${firstTable}" LIMIT 20;`;
      tablesEl.innerHTML = rows.map(([name, type]) => `
        <button class="sql-table-btn" data-table="${esc(name)}" title="${esc(type)}">
          ${icon(type === 'view' ? 'external' : 'database', 12)} ${esc(name)}
        </button>`).join('');
      tablesEl.querySelectorAll('[data-table]').forEach((b) => b.addEventListener('click', () => {
        queryEl.value = `SELECT * FROM "${b.dataset.table}" LIMIT 20;`;
        queryEl.focus();
      }));
    } catch (e) {
      tablesEl.innerHTML = `<span class="muted small">${esc(e.message)}</span>`;
    }
  }

  async function run() {
    const query = queryEl.value.trim();
    if (!query) { toast('Enter a SQL statement'); return; }
    if (DESTRUCTIVE.test(query)) {
      const ok = await confirmDialog('This statement may modify or delete data. Continue?', { okLabel: 'Run' });
      if (!ok) return;
    }
    const btn = main.querySelector('#runSql');
    btn.disabled = true;
    try {
      const res = await exec(query);
      resultCard.style.display = 'block';
      if (res.type === 'exec') {
        titleEl.textContent = 'Statement executed';
        resultEl.innerHTML = `<p class="muted">Rows affected: <b>${esc(res.rowsAffected)}</b> · ${esc(res.elapsedMs)} ms</p>`;
        loadTables();
        return;
      }
      const cols = res.columns || [];
      const rows = res.rows || [];
      titleEl.textContent = `Result · ${rows.length} row(s) · ${res.elapsedMs} ms${res.truncated ? ' · truncated' : ''}`;
      if (!rows.length) {
        resultEl.innerHTML = '<p class="muted">No rows.</p>';
        return;
      }
      resultEl.innerHTML = `<div class="table-wrap sql-result"><table class="grid">
        <thead><tr>${cols.map((c) => `<th class="no-sort">${esc(c)}</th>`).join('')}</tr></thead>
        <tbody>${rows.map((row) => `<tr>${row.map((v) => `<td>${renderValue(v)}</td>`).join('')}</tr>`).join('')}</tbody>
      </table></div>`;
    } catch (e) {
      resultCard.style.display = 'block';
      titleEl.textContent = 'Error';
      resultEl.innerHTML = `<pre class="help-box mono" style="color:var(--danger);white-space:pre-wrap">${esc(e.message)}</pre>`;
    } finally {
      btn.disabled = false;
    }
  }

  main.querySelector('#runSql').addEventListener('click', run);
  queryEl.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); run(); }
  });
  main.querySelector('#presetTables').addEventListener('click', () => {
    queryEl.value = `SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name;`;
    run();
  });
  main.querySelector('#presetDescribe').addEventListener('click', () => {
    const t = firstTable || 'collection';
    queryEl.value = `PRAGMA table_info("${t}");`;
    run();
  });
  main.querySelector('#presetCount').addEventListener('click', () => {
    const t = firstTable || 'collection';
    queryEl.value = `SELECT COUNT(*) AS total FROM "${t}";`;
    run();
  });

  await loadTables();
  queryEl.focus();
}
