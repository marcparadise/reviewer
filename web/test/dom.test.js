import test from 'node:test';
import assert from 'node:assert/strict';
import { el, setApp, formatDate, statusBadge, errMsg, btn, isDark, setTheme, toggleTheme, toast, initTheme } from '../dom.js';
import { resetDOM, setSystemDark } from './setup.js';

test.beforeEach(() => resetDOM());

test('el sets class and other attributes', () => {
  const node = el('div', { class: 'a b', id: 'x', 'data-role': 'pill' });
  assert.equal(node.tagName, 'DIV');
  assert.equal(node.className, 'a b');
  assert.equal(node.id, 'x');
  assert.equal(node.getAttribute('data-role'), 'pill');
});

test('el creates a bare element with no attrs', () => {
  assert.equal(el('span').outerHTML, '<span></span>');
});

test('setApp replaces whatever was in the app container', () => {
  const app = document.getElementById('app');
  app.appendChild(el('p'));
  setApp(el('h1'));
  assert.equal(app.childNodes.length, 1);
  assert.equal(app.firstChild.tagName, 'H1');
});

test('formatDate reads zone-less SQLite timestamps as UTC', () => {
  const utc = new Date('2026-01-02T03:04:05Z').toLocaleString();
  assert.equal(formatDate('2026-01-02 03:04:05'), utc);
  assert.equal(formatDate('2026-01-02T03:04:05Z'), utc);
});

test('formatDate returns empty for a missing timestamp', () => {
  assert.equal(formatDate(''), '');
  assert.equal(formatDate(null), '');
});

test('statusBadge humanizes the status', () => {
  assert.equal(statusBadge('changes_requested').textContent, 'changes requested');
  assert.equal(statusBadge('changes_requested').className, 'badge badge-changes_requested');
});

test('errMsg prefixes the message, falling back to the string form', () => {
  assert.equal(errMsg(new Error('boom')).textContent, 'Error: boom');
  assert.equal(errMsg('plain').textContent, 'Error: plain');
});

test('btn disables while its handler runs and re-enables after', async () => {
  let seenDisabled;
  const b = btn('Go', 'btn-primary', async () => {
    seenDisabled = b.disabled;
  });
  b.click();
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(seenDisabled, true);
  assert.equal(b.disabled, false);
});

test('btn re-enables and reports the error when its handler fails', async () => {
  const b = btn('Go', 'btn', async () => {
    throw new Error('nope');
  });
  b.click();
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(b.disabled, false);
  assert.equal(document.querySelector('.toast').textContent, 'Error: nope');
});

test('setTheme stores the choice and syncs every theme button', () => {
  const a = el('button', { 'data-theme-btn': '' });
  const b = el('button', { 'data-theme-btn': '' });
  document.body.append(a, b);

  setTheme(true);
  assert.equal(isDark(), true);
  assert.equal(localStorage.getItem('reviewer-theme'), 'dark');
  assert.equal(a.textContent, '☀');
  assert.equal(a.title, 'Light mode');

  setTheme(false);
  assert.equal(isDark(), false);
  assert.equal(localStorage.getItem('reviewer-theme'), 'light');
  assert.equal(b.textContent, '☾');
  assert.equal(b.title, 'Dark mode');
});

test('toggleTheme flips the current theme', () => {
  setTheme(false);
  toggleTheme();
  assert.equal(isDark(), true);
  toggleTheme();
  assert.equal(isDark(), false);
});

test('initTheme wires the top bar button and follows the system preference', () => {
  document.body.innerHTML = '<button id="theme-toggle-topbar"></button><main id="app"></main>';
  initTheme();

  document.getElementById('theme-toggle-topbar').click();
  assert.equal(isDark(), true);
  assert.equal(localStorage.getItem('reviewer-theme'), 'dark');

  setTheme(false);
  localStorage.clear();
  setSystemDark(true);
  assert.equal(isDark(), true);

  setTheme(false);
  setSystemDark(true);
  assert.equal(isDark(), false);
});

test('toast reuses one node and replaces its message', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  toast('first');
  const node = document.querySelector('.toast');
  assert.equal(node.textContent, 'first');
  assert.ok(node.classList.contains('toast-visible'));

  toast('second');
  assert.equal(document.querySelectorAll('.toast').length, 1);
  assert.equal(node.textContent, 'second');
});
