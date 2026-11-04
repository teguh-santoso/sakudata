import { api, setToken, state } from '../api.js';
import { esc, icon, brandMark } from '../ui.js';

export function renderLogin(root, onSuccess) {
  const setup = !!state.meta.needsSetup;
  const title = setup ? 'Create superuser' : 'Superuser login';
  const sub = setup
    ? 'Set up the first administrator account for this instance.'
    : `Sign in to manage ${esc(state.meta.appName || 'SakuData')}`;

  root.innerHTML = `
    <div class="login-wrap">
      <form class="login-card" id="loginForm" autocomplete="on">
        <div class="login-logo">${brandMark(30)}</div>
        <h1>${esc(title)}</h1>
        <p class="sub">${sub}</p>
        <div class="field">
          <label for="email">Email <span class="req">*</span></label>
          <input id="email" name="email" type="email" autocomplete="username" required autofocus>
        </div>
        <div class="field">
          <label for="password">Password <span class="req">*</span></label>
          <input id="password" name="password" type="password" autocomplete="new-password" required minlength="8">
          ${setup ? '<div class="hint">At least 8 characters.</div>' : ''}
        </div>
        ${setup ? `
        <div class="field">
          <label for="confirm">Confirm password <span class="req">*</span></label>
          <input id="confirm" name="confirm" type="password" autocomplete="new-password" required minlength="8">
        </div>` : ''}
        <button class="btn btn-primary" style="width:100%;justify-content:center;padding:11px" type="submit">
          ${setup ? 'Create account' : `Login ${icon('chevron', 16)}`}
        </button>
        <p class="login-err" id="loginErr"></p>
      </form>
    </div>`;

  const form = root.querySelector('#loginForm');
  const err = root.querySelector('#loginErr');
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    err.textContent = '';
    if (setup && form.password.value !== form.confirm.value) {
      err.textContent = 'Passwords do not match.';
      return;
    }
    const btn = form.querySelector('button[type=submit]');
    btn.disabled = true;
    try {
      let res;
      if (setup) {
        res = await api('/api/admin/setup', {
          method: 'POST',
          body: JSON.stringify({ email: form.email.value.trim(), password: form.password.value }),
        });
        state.meta.needsSetup = false;
      } else {
        res = await api('/api/admin/login', {
          method: 'POST',
          body: JSON.stringify({ email: form.email.value.trim(), password: form.password.value }),
        });
      }
      setToken(res.token);
      onSuccess();
    } catch (e2) {
      err.textContent = e2.message;
    } finally {
      btn.disabled = false;
    }
  });
}
