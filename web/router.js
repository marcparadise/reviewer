import { el, setApp } from './dom.js';

export const routes = [];

export function addRoute(pattern, fn) {
  routes.push({ re: new RegExp('^' + pattern + '$'), fn });
}

export function navigate(hash) {
  const path = (hash || '#/').replace(/^#/, '') || '/';
  for (const r of routes) {
    const m = path.match(r.re);
    if (m) { r.fn(...m.slice(1)); return; }
  }
  const c = el('div', { class: 'container' });
  c.textContent = 'Page not found.';
  setApp(c);
}

window.addEventListener('hashchange', () => navigate(location.hash));
document.addEventListener('DOMContentLoaded', () => navigate(location.hash));
