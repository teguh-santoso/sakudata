import { api, state } from '../api.js';
import { esc, icon, toast, confirmDialog, loading, errorBox } from '../ui.js';

function humanSize(n) {
  if (!n && n !== 0) return '—';
  const units = ['B', 'KB', 'MB', 'GB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

export async function renderBackups(main) {
  main.innerHTML = `
    <div class="page-head">
      <div class="crumbs">Backups</div>
      <div class="head-actions">
        <button class="btn btn-primary" id="backupNow">${icon('plus')} Back up now</button>
        <button class="btn btn-ghost btn-icon" id="reload" title="Reload">${icon('refresh')}</button>
      </div>
    </div>
    <div class="content" id="backupContent">${loading()}</div>`;
  const content = main.querySelector('#backupContent');

  async function load() {
    content.innerHTML = loading();
    try {
      const [data, settings] = await Promise.all([
        api('/api/admin/backups'),
        api('/api/admin/settings'),
      ]);
      const app = settings.app || {};
      const items = data.items || [];
      content.innerHTML = `
        <div class="card">
          <h3>Automatic backups</h3>
          <p class="card-sub">Snapshots use SQLite's VACUUM INTO for consistent copies and are uploaded to S3 when storage is enabled.</p>
          <label class="setting-row">
            <div class="grow"><div class="t">Enable automatic backups</div>
              <div class="d">Runs in the background while the server is up.</div></div>
            <input class="checkbox" type="checkbox" id="bk_enabled" ${app.backup_enabled === '1' ? 'checked' : ''}>
          </label>
          <div class="setting-row">
            <div class="grow"><div class="t">Interval (hours)</div><div class="d">How often a new snapshot is created.</div></div>
            <input class="input" id="bk_hours" type="number" min="1" style="width:100px" value="${esc(app.backup_interval_hours || 24)}">
          </div>
          <div class="setting-row">
            <div class="grow"><div class="t">Keep snapshots</div><div class="d">Older snapshots are pruned locally and in S3.</div></div>
            <input class="input" id="bk_keep" type="number" min="1" style="width:100px" value="${esc(app.backup_keep || 7)}">
          </div>
          <div class="setting-row">
            <div class="grow"><div class="t">S3 storage</div>
              <div class="d">${data.s3 ? 'Enabled — backups are uploaded to the configured bucket.' : 'Disabled — configure it under Settings → File storage.'}</div></div>
            <span class="badge ${data.s3 ? 'on' : 'off'}">${data.s3 ? 'S3 on' : 'local only'}</span>
          </div>
          <div style="display:flex;justify-content:flex-end;margin-top:10px">
            <button class="btn btn-primary" id="saveBackupSettings">${icon('check')} Save</button>
          </div>
        </div>

        <div class="card">
          <h3>Snapshots</h3>
          ${items.length ? `<div class="table-wrap"><table class="grid">
            <thead><tr><th class="no-sort">Name</th><th class="no-sort">Size</th><th class="no-sort">Created</th><th class="no-sort">Source</th><th class="no-sort"></th></tr></thead>
            <tbody>${items.map((b) => `
              <tr>
                <td class="mono small">${esc(b.name)}</td>
                <td>${humanSize(b.size)}</td>
                <td class="small">${esc(b.modified)}</td>
                <td><span class="badge ${b.source.includes('s3') ? 'on' : 'off'}">${esc(b.source)}</span></td>
                <td class="actions">
                  <button class="btn btn-sm" data-dl="${esc(b.name)}">${icon('file', 14)} Download</button>
                  <button class="btn btn-sm" data-restore="${esc(b.name)}">${icon('refresh', 14)} Restore</button>
                  <button class="btn btn-sm btn-danger" data-del="${esc(b.name)}">${icon('trash', 14)}</button>
                </td>
              </tr>`).join('')}</tbody>
          </table></div>` : '<div class="empty"><div class="big">💾</div>No snapshots yet.</div>'}
        </div>`;

      content.querySelector('#saveBackupSettings').addEventListener('click', async () => {
        try {
          await api('/api/admin/settings', {
            method: 'PUT',
            body: JSON.stringify({
              backup_enabled: content.querySelector('#bk_enabled').checked ? '1' : '0',
              backup_interval_hours: content.querySelector('#bk_hours').value.trim(),
              backup_keep: content.querySelector('#bk_keep').value.trim(),
            }),
          });
          toast('Backup settings saved');
        } catch (e) { toast(e.message); }
      });

      content.querySelectorAll('[data-dl]').forEach((b) => b.addEventListener('click', async () => {
        const name = b.dataset.dl;
        const res = await fetch(`/api/admin/backups/${encodeURIComponent(name)}`, {
          headers: { Authorization: state.token },
        });
        if (!res.ok) { toast('Download failed'); return; }
        const blob = await res.blob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url; a.download = name; a.click();
        setTimeout(() => URL.revokeObjectURL(url), 5000);
      }));

      content.querySelectorAll('[data-del]').forEach((b) => b.addEventListener('click', async () => {
        if (!(await confirmDialog(`Delete backup "${b.dataset.del}"?`, { okLabel: 'Delete' }))) return;
        try {
          await api(`/api/admin/backups/${encodeURIComponent(b.dataset.del)}`, { method: 'DELETE' });
          toast('Backup deleted');
          load();
        } catch (e) { toast(e.message); }
      }));

      content.querySelectorAll('[data-restore]').forEach((b) => b.addEventListener('click', async () => {
        const name = b.dataset.restore;
        if (!(await confirmDialog(
          `Restore "${name}"? The current data will be moved aside and the service must restart to apply the snapshot.`,
          { okLabel: 'Stage restore', danger: true },
        ))) return;
        try {
          const res = await api(`/api/admin/backups/${encodeURIComponent(name)}/restore`, { method: 'POST' });
          toast(res.message || 'Restore staged. Restart the service to apply.');
        } catch (e) { toast(e.message); }
      }));
    } catch (e) {
      content.innerHTML = errorBox(e);
    }
  }

  main.querySelector('#backupNow').addEventListener('click', async () => {
    toast('Creating backup…');
    try {
      const item = await api('/api/admin/backups', { method: 'POST' });
      toast(`Backup created: ${item.name}`);
      load();
    } catch (e) { toast(e.message); }
  });
  main.querySelector('#reload').addEventListener('click', load);
  await load();
}
