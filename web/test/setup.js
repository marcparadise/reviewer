import { JSDOM } from 'jsdom';

const dom = new JSDOM(
  '<!doctype html><html><head></head><body><main id="app"></main></body></html>',
  { url: 'http://localhost/' }
);

function define(name, value) {
  Object.defineProperty(globalThis, name, {
    value,
    writable: true,
    configurable: true,
  });
}

define('window', dom.window);
define('document', dom.window.document);
define('localStorage', dom.window.localStorage);
define('location', dom.window.location);
define('HTMLElement', dom.window.HTMLElement);
define('Element', dom.window.Element);
define('Node', dom.window.Node);
define('CustomEvent', dom.window.CustomEvent);
define('getSelection', () => dom.window.getSelection());

dom.window.Element.prototype.scrollIntoView = function () {};

let systemDark = false;
const mediaListeners = [];
dom.window.matchMedia = (query) => ({
  media: query,
  get matches() {
    return systemDark;
  },
  addEventListener(event, fn) {
    mediaListeners.push(fn);
  },
  removeEventListener(event, fn) {
    const i = mediaListeners.indexOf(fn);
    if (i >= 0) mediaListeners.splice(i, 1);
  },
});
define('matchMedia', dom.window.matchMedia);

export function setSystemDark(dark) {
  systemDark = dark;
  for (const fn of mediaListeners.slice()) fn({ matches: dark });
}

export function resetDOM() {
  dom.window.document.body.innerHTML = '<main id="app"></main>';
  dom.window.document.documentElement.className = '';
  localStorage.clear();
}

export function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

export let fetchCalls = [];

export function resetFetch() {
  fetchCalls = [];
}

export function jsonResponse(data, status = 200) {
  return {
    status,
    body: JSON.stringify(data),
    headers: { 'content-type': 'application/json' },
  };
}

export function textResponse(text, status = 200) {
  return { status, body: text, headers: { 'content-type': 'text/plain' } };
}

export function errorResponse(message, status = 500) {
  return jsonResponse({ error: message }, status);
}

export function route(method, path, response) {
  return { method, path, response };
}

export function mockFetch(routes) {
  resetFetch();
  globalThis.fetch = async (path, opts = {}) => {
    const method = opts.method || 'GET';
    fetchCalls.push({ method, path, body: opts.body === undefined ? undefined : JSON.parse(opts.body) });

    let matched;
    let fallback;
    for (const r of routes) {
      const sameMethod = r.method === method;
      const samePath = r.path instanceof RegExp ? r.path.test(path) : r.path === path;
      if (sameMethod && samePath) {
        matched = r.response;
        break;
      }
      if (!fallback && samePath) fallback = r.response;
    }
    const res = matched || fallback;
    if (!res) throw new Error('no mock route for ' + method + ' ' + path);

    const payload = Array.isArray(res) ? res.shift() : res;
    const init = {
      status: payload.status,
      statusText: payload.statusText ?? 'OK',
      headers: payload.headers,
    };
    if (payload.status === 204) return new Response(null, { status: 204 });
    return new Response(payload.body, init);
  };
}

export function callsFor(method, path) {
  return fetchCalls.filter((c) => c.method === method && c.path === path);
}
