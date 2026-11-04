import { api } from '../api.js';
import { esc, icon, toast, loading, errorBox } from '../ui.js';

export async function renderLogs(main) {
  main.innerHTML = `
    <div class="page-head">
      <div class="crumbs">Logs</div>
      <div class="head-actions">
        <select id="level" class="input" style="width:auto">
          <option value="">All levels</option>
          <option value="info">info</option>
          <option value="warn">warn</option>
          <option value="error">error</option>
        </select>
        <button class="btn btn-ghost btn-icon" id="reload" title="Reload">${icon('refresh')}</button>
      </div>
    </div>
    <div class="content" id="logContent">${loading()}</div>`;
  const content = main.querySelector('#logContent');
  const level = main.querySelector('#level');
  const st = { page: 1, perPage: 30 };

  async function load() {
    content.innerHTML = loading();
    try {
      const qs = new URLSearchParams({ page: st.page, perPage: st.perPage });
      if (level.value) qs.set('level', level.value);
      const res = await api('/api/admin/logs?' + qs);
      const items = res.items || [];
      if (!items.length) {
        content.innerHTML = '<div class="card"><div class="empty"><div class="big">📜</div>No log entries yet.</div></div>';
        return;
      }
      content.innerHTML = `
        <div class="table-wrap"><table class="grid">
          <thead><tr><th class="no-sort">Time</th><th class="no-sort">Level</th><th class="no-sort">Message</th><th class="no-sort">Actor</th><th class="no-sort">DB / collection</th></tr></thead>
          <tbody>${items.map((e) => `
            <tr>
              <td class="mono small">${esc(e.created)}</td>
              <td><span class="badge ${e.level === 'error' ? 'off' : e.level === 'warn' ? 'off' : 'on'}">${esc(e.level)}</span></td>
              <td>${esc(e.message)}</td>
              <td>${esc(e.actor)}</td>
              <td class="mono small">${esc([e.db, e.collection].filter(Boolean).join(' / '))}</td>
            </tr>`).join('')}</tbody>
        </table></div>
        <div class="pager">
          <button class="btn btn-sm" id="prev" ${res.page <= 1 ? 'disabled' : ''}>‹ Prev</button>
          <span>Page ${res.page} of ${res.totalPages || 1} · ${res.totalItems} entries</span>
          <button class="btn btn-sm" id="next" ${res.page >= res.totalPages ? 'disabled' : ''}>Next ›</button>
        </div>`;
      content.querySelector('#prev').addEventListener('click', () => { st.page--; load(); });
      content.querySelector('#next').addEventListener('click', () => { st.page++; load(); });
    } catch (e) {
      content.innerHTML = errorBox(e);
    }
  }

  main.querySelector('#reload').addEventListener('click', load);
  level.addEventListener('change', () => { st.page = 1; load(); });
  await load();
}
