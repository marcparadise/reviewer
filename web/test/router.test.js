import test from 'node:test';
import assert from 'node:assert/strict';
import { addRoute, navigate } from '../router.js';
import { resetDOM } from './setup.js';

test.beforeEach(() => resetDOM());

test('navigate calls the first matching route with its captures', () => {
  const seen = [];
  addRoute('/projects/([^/]+)', (slug) => seen.push(['project', slug]));
  addRoute('/projects/([^/]+)/(.+)', (slug, branch) => seen.push(['review', slug, branch]));

  navigate('#/projects/demo/feat/x');
  navigate('#/projects/demo');
  assert.deepEqual(seen, [
    ['review', 'demo', 'feat/x'],
    ['project', 'demo'],
  ]);
});

test('an empty hash routes to the root', () => {
  let hit = false;
  addRoute('/', () => {
    hit = true;
  });
  navigate('');
  navigate('#');
  assert.equal(hit, true);
});

test('an unmatched hash renders the not-found page', () => {
  navigate('#/nothing/matches/this');
  assert.equal(document.getElementById('app').textContent, 'Page not found.');
});

test('a hashchange event routes the hash in the URL', () => {
  const seen = [];
  addRoute('/wired/([^/]+)', (x) => seen.push(x));

  location.hash = '#/wired/from-event';
  seen.length = 0;
  window.dispatchEvent(new window.Event('hashchange'));

  assert.deepEqual(seen, ['from-event']);
});

test('the initial page load routes the hash in the URL', () => {
  const seen = [];
  addRoute('/initial', () => seen.push('initial'));

  location.hash = '#/initial';
  seen.length = 0;
  document.dispatchEvent(new window.Event('DOMContentLoaded'));

  assert.deepEqual(seen, ['initial']);
});
