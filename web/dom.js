export function el(tag, attrs) {
  const e = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (k === 'class') e.className = v;
      else e.setAttribute(k, v);
    }
  }
  return e;
}

export function setApp(node) {
  const app = document.getElementById('app');
  app.innerHTML = '';
  app.appendChild(node);
}

export function formatDate(s) {
  if (!s) return '';
  const d = new Date(s.endsWith('Z') ? s : s + 'Z');
  return d.toLocaleString();
}

export function statusBadge(status) {
  const span = el('span', { class: 'badge badge-' + status });
  span.textContent = status.replace('_', ' ');
  return span;
}

export function mergedBadge() {
  const span = el('span', { class: 'badge badge-merged' });
  span.textContent = 'merged';
  return span;
}

export function errMsg(e) {
  const div = el('div', { class: 'error-msg' });
  div.textContent = 'Error: ' + (e.message || String(e));
  return div;
}

export function btn(label, cls, onClick) {
  const b = el('button', { class: 'btn ' + cls });
  b.textContent = label;
  b.addEventListener('click', async () => {
    b.disabled = true;
    try {
      await onClick();
    } catch (e) {
      toast(errMsg(e).textContent);
    } finally {
      b.disabled = false;
    }
  });
  return b;
}

// ── Theme management ──

export function isDark() {
  return document.documentElement.classList.contains('dark');
}

export function _themeChar() { return isDark() ? '☀' : '☾'; }
export function _themeLabel() { return isDark() ? 'Light mode' : 'Dark mode'; }

export function _syncThemeBtn(b) {
  b.textContent = _themeChar();
  b.title = _themeLabel();
}

export function setTheme(dark) {
  document.documentElement.classList.toggle('dark', dark);
  localStorage.setItem('reviewer-theme', dark ? 'dark' : 'light');
  document.querySelectorAll('[data-theme-btn]').forEach(_syncThemeBtn);
}

export function toggleTheme() { setTheme(!isDark()); }

export function initTheme() {
  const topBtn = document.getElementById('theme-toggle-topbar');
  if (topBtn) {
    topBtn.setAttribute('data-theme-btn', '');
    _syncThemeBtn(topBtn);
    topBtn.addEventListener('click', toggleTheme);
  }
  // Track system preference changes when no explicit choice stored
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
    if (!localStorage.getItem('reviewer-theme')) setTheme(e.matches);
  });
}

let toastTimer = null;

export function toast(message) {
  let node = document.querySelector('.toast');
  if (!node) {
    node = el('div', { class: 'toast', role: 'status' });
    document.body.appendChild(node);
  }
  node.textContent = message;
  node.classList.add('toast-visible');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => node.classList.remove('toast-visible'), 4000);
}
