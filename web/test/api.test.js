import test from 'node:test';
import assert from 'node:assert/strict';
import { api } from '../api.js';
import { mockFetch, fetchCalls, jsonResponse, textResponse, errorResponse, route } from './setup.js';

test('sends no body or content type for a bodyless request', async () => {
  mockFetch([route('GET', '/api/projects', jsonResponse([]))]);
  await api('GET', '/api/projects');
  assert.deepEqual(fetchCalls, [{ method: 'GET', path: '/api/projects', body: undefined }]);
});

test('serializes a JSON body and sets the content type', async () => {
  mockFetch([route('POST', '/api/x', jsonResponse({ ok: true }))]);
  await api('POST', '/api/x', { body: 'hi', line_number: 3 });
  assert.deepEqual(fetchCalls, [{ method: 'POST', path: '/api/x', body: { body: 'hi', line_number: 3 } }]);
});

test('returns null for 204 responses', async () => {
  mockFetch([route('DELETE', '/api/x/1', { status: 204, body: '', headers: {} })]);
  assert.equal(await api('DELETE', '/api/x/1'), null);
});

test('parses a successful JSON response', async () => {
  mockFetch([route('GET', '/api/x', jsonResponse({ id: 7, status: 'open' }))]);
  assert.deepEqual(await api('GET', '/api/x'), { id: 7, status: 'open' });
});

test('throws the server error message from a JSON error response', async () => {
  mockFetch([route('POST', '/api/x', errorResponse('body required', 400))]);
  await assert.rejects(api('POST', '/api/x', { body: '' }), { message: 'body required' });
});

test('throws statusText when an error response is not JSON', async () => {
  mockFetch([route('GET', '/api/x', { status: 503, statusText: 'Service Unavailable', body: '<html>', headers: { 'content-type': 'text/html' } })]);
  await assert.rejects(api('GET', '/api/x'), { message: 'Service Unavailable' });
});

test('returns the raw text of a non-JSON response', async () => {
  mockFetch([route('GET', '/static.txt', textResponse('plain words'))]);
  assert.equal(await api('GET', '/static.txt'), 'plain words');
});
