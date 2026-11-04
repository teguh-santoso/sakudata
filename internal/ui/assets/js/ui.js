import { state } from './api.js';

export function esc(v) {
  return String(v ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

export function attr(v) {
  return esc(v);
}

export function fmtDate(v) {
  if (!v) return '';
  const d = new Date(v);
  if (isNaN(d.getTime())) return esc(v);
  return d.toLocaleString();
}

export function fmtValue(v, type) {
  if (v === null || v === undefined || v === '') return '<span class="muted">—</span>';
  if (type === 'bool') return v ? '<span class="badge on">true</span>' : '<span class="badge off">false</span>';
  if (type === 'json' || typeof v === 'object') return `<code class="mono">${esc(JSON.stringify(v))}</code>`;
  return esc(v);
}

const ICONS = {
  collections: '<path d="M4 5h16M4 12h16M4 19h16"/>',
  database: '<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5"/><path d="M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/>',
  settings: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1-1.5 1.7 1.7 0 0 0-1.9.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.9 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1 1.7 1.7 0 0 0-.3-1.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.9.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.9-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.9V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
  logs: '<path d="M4 4h16v16H4z"/><path d="M8 8h8M8 12h8M8 16h5"/>',
  users: '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.9"/><path d="M16 3.1a4 4 0 0 1 0 7.8"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
  trash: '<path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6"/>',
  edit: '<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/>',
  x: '<path d="M18 6 6 18M6 6l12 12"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
  moon: '<path d="M21 12.8A8.5 8.5 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/>',
  logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="m16 17 5-5-5-5M21 12H9"/>',
  refresh: '<path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/>',
  chevron: '<path d="m9 18 6-6-6-6"/>',
  file: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/>',
  external: '<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><path d="M15 3h6v6M10 14 21 3"/>',
  up: '<path d="m18 15-6-6-6 6"/>',
  down: '<path d="m6 9 6 6 6-6"/>',
  check: '<path d="M20 6 9 17l-5-5"/>',
  menu: '<path d="M4 6h16M4 12h16M4 18h16"/>',
  window: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18"/><path d="M7 6.5h.01M10 6.5h.01"/>',
  key: '<circle cx="7.5" cy="15.5" r="4.5"/><path d="m11 12 9-9M17 6l3 3M14 9l3 3"/>',
  terminal: '<path d="m4 17 6-5-6-5M12 19h8"/>',
};

// Pocket mark ("saku") — a rounded pocket with a stitched opening and button.
export function brandMark(size = 18) {
  return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" aria-hidden="true">
    <path d="M5 5.5h14a1 1 0 0 1 1 1v7.2A6.3 6.3 0 0 1 13.7 20h-3.4A6.3 6.3 0 0 1 4 13.7V6.5a1 1 0 0 1 1-1z" fill="currentColor"/>
    <path d="M8 5.8c.4 3 1.8 4.7 4 4.7s3.6-1.7 4-4.7" fill="none" stroke="rgba(0,0,0,.30)" stroke-width="2.2" stroke-linecap="round"/>
    <circle cx="12" cy="15.2" r="1.5" fill="rgba(0,0,0,.30)"/>
  </svg>`;
}

export function icon(name, size = 16) {
  return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[name] || ''}</svg>`;
}

export function applyTheme(theme) {
  state.theme = theme;
  document.documentElement.dataset.theme = theme;
  localStorage.setItem('mdb_theme', theme);
}

export function toggleTheme() {
  applyTheme(state.theme === 'dark' ? 'light' : 'dark');
}

let toastTimer;
export function toast(message) {
  const el = document.getElementById('toast');
  if (!el) return;
  el.textContent = message;
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), 2600);
}

export function panel({ title, bodyHtml = '', footHtml = '', drawer = false, onMount }) {
  const overlay = document.createElement('div');
  overlay.className = 'overlay';
  overlay.innerHTML = `
    <div class="${drawer ? 'drawer' : 'modal-wrap'}" role="dialog" aria-modal="true">
      <div class="${drawer ? 'drawer-head' : 'modal-head'}">
        <h3>${esc(title)}</h3><span class="spacer"></span>
        <button class="btn btn-ghost btn-icon" data-close title="Close">${icon('x')}</button>
      </div>
      <div class="${drawer ? 'drawer-body' : 'modal-body'}">${bodyHtml}</div>
      ${footHtml ? `<div class="${drawer ? 'drawer-foot' : 'modal-foot'}">${footHtml}</div>` : ''}
    </div>`;
  document.body.appendChild(overlay);
  const box = overlay.firstElementChild;
  const close = () => {
    overlay.remove();
    document.removeEventListener('keydown', onKey);
  };
  const onKey = (e) => { if (e.key === 'Escape') close(); };
  document.addEventListener('keydown', onKey);
  overlay.addEventListener('click', (e) => {
    if (e.target === overlay || e.target.closest('[data-close]')) close();
  });
  if (onMount) onMount(box, close);
  return { overlay, box, close };
}

export function confirmDialog(message, { danger = true, okLabel = 'Confirm' } = {}) {
  return new Promise((resolve) => {
    const { overlay, box, close } = panel({
      title: 'Please confirm',
      bodyHtml: `<p style="margin:0;line-height:1.55">${esc(message)}</p>`,
      footHtml: `<button class="btn" data-close>Cancel</button>
        <button class="btn ${danger ? 'btn-danger' : 'btn-primary'}" data-ok>${esc(okLabel)}</button>`,
    });
    box.querySelector('[data-ok]').addEventListener('click', () => { resolve(true); close(); });
    box.querySelectorAll('[data-close]').forEach((b) => b.addEventListener('click', () => resolve(false)));
    overlay.addEventListener('click', (e) => { if (e.target === overlay) resolve(false); });
  });
}

export function loading(text = 'Loading…') {
  return `<div class="empty"><div class="big">⏳</div>${esc(text)}</div>`;
}

export function errorBox(err) {
  return `<div class="empty"><div class="big">⚠️</div>${esc(err.message || err)}</div>`;
}
