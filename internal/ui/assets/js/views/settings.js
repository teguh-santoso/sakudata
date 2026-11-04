import { api, state } from '../api.js';
import { esc, icon, toast, loading, errorBox } from '../ui.js';

export async function renderSettings(main) {
  main.innerHTML = `${head('Settings')}<div class="content" id="settingsContent">${loading()}</div>`;
  const content = main.querySelector('#settingsContent');
  try {
    const data = await api('/api/admin/settings');
    const app = data.app || {};
    const s3 = data.s3 || {};
    content.innerHTML = `
      <div class="card">
        <h3>Application</h3>
        <p class="card-sub">Branding and public URL used in links.</p>
        <div class="field"><label for="app_name">Application name</label>
          <input id="app_name" value="${esc(app.app_name)}" placeholder="SakuData"></div>
        <div class="field"><label for="app_base_url">Base URL</label>
          <input id="app_base_url" value="${esc(app.app_base_url)}" placeholder="https://app.example.com"></div>
      </div>

      <div class="card">
        <h3>File storage</h3>
        <p class="card-sub">Local disk by default. Enable S3-compatible storage to store uploads remotely.</p>
        <label class="setting-row">
          <div class="grow"><div class="t">Use S3 storage</div><div class="d">Currently: ${esc(data.backend)}</div></div>
          <input class="checkbox" type="checkbox" id="s3_enabled" ${s3.enabled === '1' ? 'checked' : ''}>
        </label>
        <div class="field"><label for="s3_endpoint">Endpoint</label><input id="s3_endpoint" value="${esc(s3.endpoint)}" placeholder="https://s3.example.com"></div>
        <div class="field"><label for="s3_region">Region</label><input id="s3_region" value="${esc(s3.region)}" placeholder="auto"></div>
        <div class="field"><label for="s3_bucket">Bucket</label><input id="s3_bucket" value="${esc(s3.bucket)}"></div>
        <div class="field"><label for="s3_access">Access key</label><input id="s3_access" value="${esc(s3.access)}"></div>
        <div class="field"><label for="s3_secret">Secret key</label><input id="s3_secret" type="password" placeholder="(unchanged)"></div>
        <div class="field"><label for="s3_prefix">Prefix</label><input id="s3_prefix" value="${esc(s3.prefix)}" placeholder="optional/path"></div>
        <label class="setting-row">
          <div class="grow"><div class="t">Force path style</div><div class="d">Enable for MinIO and most self-hosted gateways.</div></div>
          <input class="checkbox" type="checkbox" id="s3_pathstyle" ${s3.pathstyle === '1' ? 'checked' : ''}>
        </label>
      </div>

      <div class="card">
        <h3>Mail</h3>
        <p class="card-sub">Used for verification and password reset in future releases.</p>
        <div class="field"><label for="mail_from">From address</label><input id="mail_from" value="${esc(app.mail_from)}" placeholder="noreply@example.com"></div>
        <div class="field"><label for="mail_host">SMTP host</label><input id="mail_host" value="${esc(app.mail_host)}"></div>
        <div class="field"><label for="mail_port">SMTP port</label><input id="mail_port" value="${esc(app.mail_port)}" placeholder="587"></div>
        <div class="field"><label for="mail_user">SMTP user</label><input id="mail_user" value="${esc(app.mail_user)}"></div>
        <div class="field"><label for="mail_pass">SMTP password</label><input id="mail_pass" type="password" placeholder="(unchanged)"></div>
      </div>

      <div style="display:flex;justify-content:flex-end">
        <button class="btn btn-primary" id="saveSettings">${icon('check')} Save settings</button>
      </div>`;

    content.querySelector('#saveSettings').addEventListener('click', async () => {
      const val = (id) => content.querySelector(`#${id}`).value.trim();
      const body = {
        app_name: val('app_name'),
        app_base_url: val('app_base_url'),
        mail_from: val('mail_from'),
        mail_host: val('mail_host'),
        mail_port: val('mail_port'),
        mail_user: val('mail_user'),
        s3_endpoint: val('s3_endpoint'),
        s3_region: val('s3_region'),
        s3_bucket: val('s3_bucket'),
        s3_access: val('s3_access'),
        s3_prefix: val('s3_prefix'),
        s3_enabled: content.querySelector('#s3_enabled').checked ? '1' : '0',
        s3_pathstyle: content.querySelector('#s3_pathstyle').checked ? '1' : '0',
      };
      const secret = content.querySelector('#s3_secret').value;
      if (secret) body.s3_secret = secret;
      const mailPass = content.querySelector('#mail_pass').value;
      if (mailPass) body.mail_pass = mailPass;
      try {
        await api('/api/admin/settings', { method: 'PUT', body: JSON.stringify(body) });
        toast('Settings saved');
        state.meta.appName = body.app_name || state.meta.appName;
      } catch (e) { toast(e.message); }
    });
  } catch (e) {
    content.innerHTML = errorBox(e);
  }
}

function head(title) {
  return `<div class="page-head"><div class="crumbs">${esc(title)}</div></div>`;
}
