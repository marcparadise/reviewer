import test from 'node:test';
import assert from 'node:assert/strict';
import {
  commentableLine, noCommentReason, anchorLine, buildCommentMap, strayComments,
  renderDiffView, buildSideBySideHunkRows,
} from '../diff.js';
import { formatDate } from '../dom.js';
import { resetDOM, mockFetch, route, jsonResponse, errorResponse, callsFor, flush, fetchCalls } from './setup.js';

test.beforeEach(() => resetDOM());

const CTX = { slug: 'proj', reviewId: 7, headSha: 'abc123' };

function reviewCtx(over = {}) {
  return {
    slug: 'proj',
    reviewId: 7,
    headSha: 'abc123',
    highlightEnabled: false,
    canComment: true,
    commentAnchor: null,
    ...over,
  };
}

function ctxLine(left, right, content) {
  return { type: 'ctx', left, right, content };
}

function addLine(right, content) {
  return { type: 'add', left: null, right, content };
}

function delLine(left, content) {
  return { type: 'del', left, right: null, content };
}

function baseLines() {
  return [
    ctxLine(1, 1, 'one'),
    delLine(2, 'two-old'),
    addLine(2, 'two-new'),
    ctxLine(3, 3, 'three'),
    ctxLine(4, 4, 'four'),
  ];
}

function makeFile(over = {}) {
  return {
    path: 'a.js',
    old_path: 'a.js',
    status: 'M',
    binary: false,
    hunks: [{ header: '@@ -1,4 +1,4 @@', start_right: 1, end_right: 5, lines: baseLines() }],
    ...over,
  };
}

function gapFile(secondStart) {
  return makeFile({
    hunks: [
      {
        header: '@@ -1,4 +1,4 @@',
        start_right: 1,
        end_right: 5,
        lines: [ctxLine(1, 1, 'one'), ctxLine(2, 2, 'two'), ctxLine(3, 3, 'three'), ctxLine(4, 4, 'four')],
      },
      {
        header: '@@ -' + secondStart + ',2 +' + secondStart + ',2 @@',
        start_right: secondStart,
        end_right: secondStart + 2,
        lines: [ctxLine(secondStart, secondStart, 'l' + secondStart), ctxLine(secondStart + 1, secondStart + 1, 'l' + (secondStart + 1))],
      },
    ],
  });
}

function mount(files, comments, over = {}, opts = {}) {
  const rctx = reviewCtx(over);
  const fullFileMode = opts.fullFileMode || new Map();
  const collapseMap = opts.collapseMap || new Map();
  const diffState = { el: null };
  diffState.el = renderDiffView(files, comments, rctx, fullFileMode, collapseMap, opts.diffMode || 'unified', diffState);
  document.body.appendChild(diffState.el);
  return { rctx, fullFileMode, collapseMap, diffState, el: diffState.el };
}

function contentPath(extra = '') {
  return '/api/projects/' + CTX.slug + '/reviews/' + CTX.reviewId + '/file-content?path=a.js&sha=abc123' + extra;
}

function rowFor(root, n) {
  return root.querySelector('tr.diff-line[data-right="' + n + '"]');
}

function shiftClick(node) {
  node.dispatchEvent(new window.MouseEvent('click', { shiftKey: true, bubbles: true }));
}

function comment(over = {}) {
  return {
    id: 9,
    file_path: 'a.js',
    line_number: 2,
    line_end: null,
    body: 'note',
    created_at: '2026-01-02 03:04:05',
    ...over,
  };
}

test('commentableLine only allows lines still present on the right side', () => {
  const rctx = reviewCtx();
  assert.equal(commentableLine(addLine(5, 'x'), rctx), true);
  assert.equal(commentableLine(ctxLine(4, 5, 'x'), rctx), true);
  assert.equal(commentableLine(delLine(4, 'x'), rctx), false);
  assert.equal(commentableLine(null, rctx), false);
  assert.equal(commentableLine(addLine(5, 'x'), reviewCtx({ canComment: false })), false);
});

test('noCommentReason names the blocker for each kind of line', () => {
  assert.match(noCommentReason(addLine(5, 'x'), reviewCtx({ canComment: false })), /commits are selected/);
  assert.match(noCommentReason(delLine(4, 'x'), reviewCtx()), /Removed lines can't be commented on/);
  assert.equal(noCommentReason(null, reviewCtx()), null);
  assert.equal(noCommentReason(addLine(5, 'x'), reviewCtx()), null);
});

test('anchorLine keys comments by the end of a range', () => {
  assert.equal(anchorLine({ line_number: 4 }), 4);
  assert.equal(anchorLine({ line_number: 4, line_end: 9 }), 9);
  assert.equal(anchorLine({ line_number: null, line_end: null }), null);
});

test('buildCommentMap keys by anchored line and skips comments with no file or line', () => {
  const map = buildCommentMap([
    { id: 1, file_path: 'a.js', line_number: 4, line_end: null },
    { id: 2, file_path: 'a.js', line_number: 4, line_end: 6 },
    { id: 3, file_path: 'a.js', line_number: null, line_end: null },
    { id: 4, file_path: '', line_number: 3, line_end: null },
    { id: 5, file_path: 'b.js', line_number: 4, line_end: null },
  ]);
  assert.deepEqual(Object.keys(map).sort(), ['a.js:4', 'a.js:6', 'b.js:4']);
  assert.deepEqual(map['a.js:4'].map(c => c.id), [1]);
  assert.deepEqual(map['a.js:6'].map(c => c.id), [2]);
  assert.deepEqual(map['b.js:4'].map(c => c.id), [5]);
});

test('strayComments keeps file-level and off-screen comments only', () => {
  const comments = [
    { id: 1, file_path: 'a.js', line_number: null, line_end: null },
    { id: 2, file_path: 'a.js', line_number: 40, line_end: null },
    { id: 3, file_path: 'a.js', line_number: 5, line_end: null },
    { id: 4, file_path: 'b.js', line_number: 40, line_end: null },
    { id: 5, file_path: 'a.js', line_number: 3, line_end: 6 },
  ];
  const rendered = new Set([5, 6, 7]);
  assert.deepEqual(strayComments(comments, 'a.js', rendered, false).map(c => c.id), [1, 2]);
  assert.deepEqual(strayComments(comments, 'a.js', rendered, true).map(c => c.id), [1]);
});

test('renderDiffView labels renames and deletions in the file header', () => {
  const files = [
    makeFile({ path: 'src/new-name.js', old_path: 'src/old-name.js', status: 'R' }),
    makeFile({ path: 'gone.js', old_path: 'gone.js', status: 'D' }),
  ];
  const { el } = mount(files, []);
  assert.deepEqual(
    [...el.querySelectorAll('.diff-file-name')].map(n => n.textContent),
    ['src/old-name.js → src/new-name.js', 'gone.js']
  );
  assert.deepEqual(
    [...el.querySelectorAll('.file-tag')].map(t => t.className + '/' + t.textContent),
    ['file-tag file-tag-renamed/renamed', 'file-tag file-tag-deleted/deleted']
  );
  assert.deepEqual([...el.querySelectorAll('.diff-file')].map(f => f.id), ['file-src_new_name_js', 'file-gone_js']);
});

test('renderDiffView hides Full file for deleted and binary files', () => {
  const files = [
    makeFile({ path: 'gone.js', old_path: 'gone.js', status: 'D' }),
    makeFile({ path: 'img.png', old_path: 'img.png', binary: true, hunks: [] }),
    makeFile({ path: 'plain.js', old_path: 'plain.js' }),
  ];
  const { el } = mount(files, []);
  const fileEls = [...el.querySelectorAll('.diff-file')];
  const withBtn = fileEls.filter(f => f.querySelector('.file-fullfile-btn'));
  assert.equal(withBtn.length, 1);
  assert.equal(withBtn[0].querySelector('.diff-file-name').textContent, 'plain.js');
  assert.equal(withBtn[0].querySelector('.file-fullfile-btn').textContent, 'Full file');
  assert.equal(fileEls[1].querySelector('.diff-binary').textContent, 'Binary file — content not shown');
  assert.equal(fileEls[1].querySelector('.diff-table'), null);
  assert.ok(fileEls[0].querySelector('.diff-table'));
});

test('renderDiffView reports files with no hunks instead of an empty body', () => {
  const files = [
    makeFile({ path: 'renamed.js', old_path: 'prev.js', status: 'R', hunks: [] }),
    makeFile({ path: 'mode.js', old_path: 'mode.js', hunks: [] }),
  ];
  const { el } = mount(files, []);
  assert.deepEqual(
    [...el.querySelectorAll('.diff-binary')].map(n => n.textContent),
    ['Renamed with no content changes', 'No content changes']
  );
  assert.equal(el.querySelectorAll('.diff-table').length, 0);
});

test('renderDiffView places comment rows after the line they are anchored to', () => {
  const comments = [
    comment({ id: 9, line_number: 2 }),
    comment({ id: 10, line_number: 1, line_end: 3, body: 'range' }),
  ];
  const { el } = mount([makeFile()], comments);
  const single = rowFor(el, 2);
  assert.equal(single.nextElementSibling.className, 'comment-row');
  assert.equal(single.nextElementSibling.dataset.commentId, '9');
  assert.equal(single.nextElementSibling.querySelector('.comment-body').textContent, 'note');
  assert.equal(single.nextElementSibling.querySelector('.comment-meta').textContent, formatDate('2026-01-02 03:04:05'));
  assert.ok(single.nextElementSibling.querySelector('.comment-delete'));

  const range = rowFor(el, 3);
  assert.equal(range.nextElementSibling.dataset.commentId, '10');
  assert.match(range.nextElementSibling.querySelector('.comment-meta').textContent, /^lines 1–3 · /);
});

test('renderDiffView drops comment rows and disables lines when comments are off', () => {
  const comments = [comment({ id: 9, line_number: 2 }), comment({ id: 11, line_number: null, body: 'file note' })];
  const { el } = mount([makeFile()], comments, { canComment: false });
  assert.equal(el.querySelectorAll('.comment-row').length, 0);
  assert.equal(el.querySelectorAll('.file-level-comment').length, 0);
  const rows = [...el.querySelectorAll('tr.diff-line')];
  assert.equal(rows.length, 5);
  assert.ok(rows.every(r => r.classList.contains('diff-line-static')));
});

test('clicking a file header collapses the body and records the state', () => {
  const collapseMap = new Map();
  const { el } = mount([makeFile()], [], {}, { collapseMap });
  const header = el.querySelector('.diff-file-header');
  const body = el.querySelector('.diff-file-body');
  const btn = el.querySelector('.file-collapse-btn');

  assert.equal(body.classList.contains('hidden'), false);
  assert.equal(btn.textContent, '▼');
  assert.equal(btn.title, 'Collapse');

  header.click();
  assert.equal(body.classList.contains('hidden'), true);
  assert.equal(btn.textContent, '▶');
  assert.equal(btn.title, 'Expand');
  assert.equal(collapseMap.get('a.js'), true);

  header.click();
  assert.equal(body.classList.contains('hidden'), false);
  assert.equal(btn.textContent, '▼');
  assert.equal(collapseMap.get('a.js'), false);
});

test('renderDiffView starts collapsed when the collapse map says so', () => {
  const { el } = mount([makeFile()], [], {}, { collapseMap: new Map([['a.js', true]]) });
  assert.equal(el.querySelector('.diff-file-body').classList.contains('hidden'), true);
  assert.equal(el.querySelector('.file-collapse-btn').textContent, '▶');
  assert.equal(el.querySelector('.file-collapse-btn').title, 'Expand');
});

test('renderDiffView offers expand rows spanning each gap between hunks', () => {
  const { el } = mount([gapFile(41)], []);
  const expands = [...el.querySelectorAll('tr.diff-expand-row')];
  assert.equal(expands.length, 2);
  assert.deepEqual(expands.map(r => [r.dataset.fromLine, r.dataset.toLine]), [['5', '40'], ['5', '40']]);
  assert.equal(expands[0].textContent, '▼ 20 lines');
  assert.equal(expands[1].textContent, '▲ 20 lines');

  const kids = [...el.querySelector('tbody').children];
  assert.deepEqual(kids.map(r => r.className), [
    'diff-hunk',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
    'diff-expand-row',
    'diff-expand-row',
    'diff-hunk',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
  ]);
  assert.equal(kids[0].textContent, '@@ -1,4 +1,4 @@');
  assert.equal(kids[7].textContent, '@@ -41,2 +41,2 @@');
});

test('adjacent hunks produce no expand row', () => {
  const file = makeFile({
    hunks: [
      { header: '@@ -1,2 +1,2 @@', start_right: 1, end_right: 3, lines: [ctxLine(1, 1, 'one'), ctxLine(2, 2, 'two')] },
      { header: '@@ -3,2 +3,2 @@', start_right: 3, end_right: 5, lines: [ctxLine(3, 3, 'three'), ctxLine(4, 4, 'four')] },
    ],
  });
  const { el } = mount([file], []);
  assert.equal(el.querySelectorAll('tr.diff-expand-row').length, 0);
});

test('the Full file button re-renders in place without collapsing the file', async () => {
  mockFetch([route('GET', contentPath(), jsonResponse({ start: 1, lines: ['one', 'two'], html: null }))]);
  const fullFileMode = new Map();
  const collapseMap = new Map();
  const m = mount([makeFile()], [], {}, { fullFileMode, collapseMap });
  const before = m.el;

  before.querySelector('.file-fullfile-btn').click();

  const after = document.body.querySelector('.diff-wrapper');
  assert.notEqual(after, before);
  assert.equal(document.body.contains(before), false);
  assert.equal(document.body.querySelectorAll('.diff-wrapper').length, 1);
  assert.equal(m.diffState.el, after);
  assert.equal(after.querySelector('.file-fullfile-btn').textContent, 'Hunks only');
  assert.equal(after.querySelector('.diff-file-body').classList.contains('hidden'), false);
  assert.equal(collapseMap.has('a.js'), false);

  await flush();
  assert.equal(callsFor('GET', contentPath()).length, 1);
  assert.equal(after.querySelector('.muted'), null);
  assert.equal(after.querySelectorAll('tr.diff-line').length, 2);
});

test('buildSideBySideHunkRows pairs an uneven del/add run with filler cells', () => {
  const hunk = {
    header: '@@ -10,3 +10,2 @@',
    start_right: 10,
    end_right: 12,
    lines: [delLine(10, 'x'), delLine(11, 'y'), addLine(10, 'X'), ctxLine(12, 11, 'z')],
  };
  const rows = buildSideBySideHunkRows(hunk, 'a.js', {}, reviewCtx());
  assert.equal(rows.length, 3);
  const [paired, filler, ctx] = rows;

  assert.equal(paired.querySelector('.ln-left').textContent, '10');
  assert.equal(paired.querySelector('.code-left').textContent, 'x');
  assert.ok(paired.querySelector('.code-left').classList.contains('diff-del'));
  assert.equal(paired.querySelector('.ln-right').textContent, '10');
  assert.equal(paired.querySelector('.code-right').textContent, 'X');
  assert.ok(paired.querySelector('.code-right').classList.contains('diff-add'));
  assert.equal(paired.dataset.right, '10');

  assert.equal(filler.querySelector('.ln-left').textContent, '11');
  assert.equal(filler.querySelector('.code-left').textContent, 'y');
  assert.equal(filler.querySelector('.ln-right').textContent, '');
  assert.equal(filler.querySelector('.code-right').textContent, '');
  assert.equal(filler.querySelector('.code-right').className, 'code code-right');
  assert.equal(filler.dataset.right, undefined);

  assert.equal(ctx.querySelector('.ln-left').textContent, '12');
  assert.equal(ctx.querySelector('.code-left').textContent, 'z');
  assert.equal(ctx.querySelector('.ln-right').textContent, '11');
  assert.equal(ctx.querySelector('.code-right').textContent, 'z');
});

test('side-by-side takes comments from the right-hand cell only', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { el } = mount([makeFile()], [], {}, { diffMode: 'side-by-side' });
  assert.ok(el.querySelector('table').classList.contains('diff-sbs'));

  const paired = el.querySelector('tr.diff-line .code-left.diff-del').closest('tr');
  paired.querySelector('.ln-left').click();
  assert.equal(el.querySelectorAll('tr.comment-form-row').length, 0);
  assert.match(document.querySelector('.toast').textContent, /Removed lines can't be commented on/);

  paired.querySelector('.code-right').click();
  const form = el.querySelector('tr.comment-form-row');
  assert.ok(form);
  assert.equal(form.previousElementSibling.className, 'diff-line');
  assert.equal(form.previousElementSibling.dataset.right, '2');
  assert.equal(form.querySelector('textarea').placeholder, 'Leave a comment… (shift-click another line to extend)');
});

test('side-by-side renders highlighted html and inlines comments on every row kind', () => {
  const marked = (content) => '<b>' + content + '</b>';
  const hunk = {
    header: '@@ -1,3 +1,3 @@',
    start_right: 1,
    end_right: 4,
    lines: [
      { type: 'ctx', left: 1, right: 1, content: 'one', html: marked('one') },
      { type: 'del', left: 2, right: null, content: 'two-old', html: marked('two-old') },
      { type: 'add', left: null, right: 2, content: 'two-new', html: marked('two-new') },
      { type: 'ctx', left: 3, right: 3, content: 'three', html: marked('three') },
    ],
  };
  const comments = [comment({ id: 9, line_number: 2, body: 'on the add' }), comment({ id: 10, line_number: 3, body: 'on the ctx' })];
  const { el } = mount([makeFile({ hunks: [hunk] })], comments, {}, { diffMode: 'side-by-side' });

  const rows = [...el.querySelectorAll('tr.diff-line')];
  assert.equal(rows.length, 3);
  assert.equal(rows[0].querySelector('.code-left').innerHTML, '<b>one</b>');
  assert.equal(rows[0].querySelector('.code-right').innerHTML, '<b>one</b>');
  assert.equal(rows[1].querySelector('.code-left').innerHTML, '<b>two-old</b>');
  assert.equal(rows[1].querySelector('.code-right').innerHTML, '<b>two-new</b>');

  const kids = [...el.querySelector('tbody').children];
  assert.deepEqual(kids.map(r => r.className), ['diff-hunk', 'diff-line', 'diff-line', 'comment-row', 'diff-line', 'comment-row']);
  assert.deepEqual([...el.querySelectorAll('.comment-body')].map(b => b.textContent), ['on the add', 'on the ctx']);

  rows[0].querySelector('.code-right').click();
  assert.equal(el.querySelector('tr.comment-form-row').previousElementSibling.dataset.right, '1');
});

test('side-by-side add-only rows ignore clicks on the left filler cell', () => {
  const file = makeFile({
    hunks: [{ header: '@@ -1,1 +1,2 @@', start_right: 1, end_right: 3, lines: [addLine(1, 'X'), addLine(2, 'Y')] }],
  });
  const { el } = mount([file], [comment({ id: 9, line_number: 2, body: 'on Y' })], {}, { diffMode: 'side-by-side' });
  const rows = [...el.querySelectorAll('tr.diff-line')];
  const addRow = rows[0];
  assert.equal(addRow.querySelector('.code-right').textContent, 'X');
  assert.equal(rows[1].querySelector('.code-right').textContent, 'Y');
  assert.deepEqual([...el.querySelector('tbody').children].map(r => r.className), ['diff-hunk', 'diff-line', 'diff-line', 'comment-row']);
  assert.equal(rows[1].nextElementSibling.querySelector('.comment-body').textContent, 'on Y');

  addRow.querySelector('.ln-left').click();
  assert.equal(el.querySelectorAll('tr.comment-form-row').length, 0);
  assert.equal(document.querySelector('.toast'), null);

  addRow.querySelector('.code-right').click();
  assert.equal(el.querySelectorAll('tr.comment-form-row').length, 1);
});

test('renderFullFile requests the whole file and renders the returned html', async () => {
  mockFetch([route('GET', contentPath('&highlight=true'), jsonResponse({
    start: 1,
    lines: ['one', 'two', 'three'],
    html: ['<b>one</b>', '<b>two</b>', null],
  }))]);
  const m = mount([makeFile()], [], { highlightEnabled: true }, { fullFileMode: new Map([['a.js', true]]) });
  assert.equal(m.el.querySelector('.muted').textContent, 'Loading full file…');

  await flush();

  assert.equal(callsFor('GET', contentPath('&highlight=true')).length, 1);
  assert.equal(m.el.querySelector('.muted'), null);
  const rows = [...m.el.querySelectorAll('tr.diff-line')];
  assert.equal(rows.length, 3);
  assert.equal(rows[0].dataset.right, '1');
  assert.equal(rows[0].className, 'diff-line diff-ctx');
  assert.equal(rows[0].querySelector('.marker').textContent, ' ');
  assert.equal(rows[0].querySelector('.code').innerHTML, '<b>one</b>');
  assert.equal(rows[1].dataset.type, 'add');
  assert.ok(rows[1].classList.contains('diff-add'));
  assert.equal(rows[1].querySelector('.marker').textContent, '+');
  assert.equal(rows[1].querySelector('.code').innerHTML, '<b>two</b>');
  assert.equal(rows[2].querySelector('.code').textContent, 'three');

  rows[1].click();
  const form = m.el.querySelector('tr.comment-form-row');
  assert.equal(form.previousElementSibling.dataset.right, '2');
  assert.deepEqual(m.rctx.commentAnchor, { path: 'a.js', line: 2 });
});

test('renderFullFile omits the highlight parameter when highlighting is off', async () => {
  mockFetch([route('GET', /\/file-content\?/, jsonResponse({ start: 1, lines: ['one'], html: null }))]);
  const m = mount([makeFile()], [], {}, { fullFileMode: new Map([['a.js', true]]) });
  await flush();
  assert.equal(callsFor('GET', contentPath()).length, 1);
  assert.equal(m.el.querySelector('.code').textContent, 'one');
});

test('renderFullFile keeps file-level notes and comments on visible lines', async () => {
  mockFetch([route('GET', /\/file-content\?/, jsonResponse({ start: 1, lines: ['one', 'two', 'three'], html: null }))]);
  const comments = [
    comment({ id: 9, line_number: 2, body: 'on the add' }),
    comment({ id: 10, line_number: 40, body: 'hidden line' }),
    comment({ id: 11, line_number: null, body: 'file note' }),
  ];
  const m = mount([makeFile()], comments, {}, { fullFileMode: new Map([['a.js', true]]) });
  await flush();

  assert.equal(m.el.querySelectorAll('.file-level-comment').length, 1);
  assert.equal(
    m.el.querySelector('.file-level-note').textContent,
    'On this file — the line it was written against is gone.'
  );
  assert.equal(rowFor(m.el, 2).nextElementSibling.dataset.commentId, '9');
  assert.deepEqual([...m.el.querySelectorAll('.comment-body')].map(b => b.textContent), ['file note', 'on the add']);
  assert.equal(m.el.textContent.includes('hidden line'), false);
});

test('renderFullFile reports a failed load', async () => {
  mockFetch([route('GET', /\/file-content\?/, errorResponse('no such file', 500))]);
  const m = mount([makeFile()], [], {}, { fullFileMode: new Map([['a.js', true]]) });
  await flush();
  assert.equal(m.el.querySelector('.muted').textContent, 'Error loading file: no such file');
  assert.equal(m.el.querySelector('.diff-table'), null);
});

test('expanding above inserts the bottom of the gap before the next hunk', async () => {
  const fetched = Array.from({ length: 20 }, (_, i) => 'line ' + (21 + i));
  mockFetch([route('GET', contentPath('&start=21&count=20'), jsonResponse({ start: 21, lines: fetched }))]);
  const { el } = mount([gapFile(41)], []);
  const above = el.querySelectorAll('tr.diff-expand-row')[1];
  const btn = above.querySelector('button');

  btn.click();
  assert.equal(btn.disabled, true);
  await flush();

  assert.equal(callsFor('GET', contentPath('&start=21&count=20')).length, 1);
  assert.equal(btn.disabled, false);
  assert.equal(btn.textContent, '▲ 16 lines');
  assert.equal(above.dataset.fromLine, '5');
  assert.equal(above.dataset.toLine, '20');

  const tbody = el.querySelector('tbody');
  const hunkHeader = tbody.querySelectorAll('tr.diff-hunk')[1];
  const kids = [...tbody.children];
  const inserted = kids.slice(kids.indexOf(above) + 1, kids.indexOf(hunkHeader));
  assert.equal(inserted.length, 20);
  assert.deepEqual(inserted.map(r => r.className), Array.from({ length: 20 }, () => 'diff-line diff-ctx'));
  assert.deepEqual(inserted.map(r => r.children[1].textContent), Array.from({ length: 20 }, (_, i) => String(21 + i)));
  assert.deepEqual(inserted.map(r => r.children[3].textContent), fetched);
});

test('expanding below inserts the top of the gap and carries the expand row along', async () => {
  const fetched = Array.from({ length: 20 }, (_, i) => 'line ' + (5 + i));
  mockFetch([route('GET', contentPath('&start=5&count=20'), jsonResponse({ start: 5, lines: fetched }))]);
  const { el } = mount([gapFile(41)], []);
  const below = el.querySelectorAll('tr.diff-expand-row')[0];
  const btn = below.querySelector('button');

  btn.click();
  await flush();

  assert.equal(callsFor('GET', contentPath('&start=5&count=20')).length, 1);
  assert.equal(btn.textContent, '▼ 16 lines');
  assert.deepEqual([below.dataset.fromLine, below.dataset.toLine], ['25', '40']);
  assert.equal(below.nextElementSibling.textContent, '▲ 20 lines');

  const tbody = el.querySelector('tbody');
  const kids = [...tbody.children];
  const at = kids.indexOf(below);
  const inserted = kids.slice(at - 20, at);
  assert.deepEqual(inserted.map(r => r.children[1].textContent), Array.from({ length: 20 }, (_, i) => String(5 + i)));
  assert.deepEqual(inserted.map(r => r.children[3].textContent), fetched);
  assert.equal(inserted[0].previousElementSibling.children[1].textContent, '4');
  assert.equal(inserted[0].previousElementSibling.className, 'diff-line diff-ctx');
});

test('expanding the rest of a gap removes the expand row', async () => {
  const fetched = ['l5', 'l6', 'l7', 'l8'];
  mockFetch([route('GET', contentPath('&start=5&count=4'), jsonResponse({ start: 5, lines: fetched }))]);
  const { el } = mount([gapFile(9)], []);
  const expands = [...el.querySelectorAll('tr.diff-expand-row')];
  assert.deepEqual(expands.map(r => r.textContent), ['▼ 4 lines', '▲ 4 lines']);

  const above = expands[1];
  above.querySelector('button').click();
  await flush();

  assert.equal(callsFor('GET', contentPath('&start=5&count=4')).length, 1);
  assert.equal(el.contains(above), false);
  assert.deepEqual([...el.querySelectorAll('tr.diff-expand-row')].map(r => r.textContent), ['▼ 4 lines']);

  const kids = [...el.querySelector('tbody').children];
  assert.deepEqual(kids.map(r => r.className), [
    'diff-hunk',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
    'diff-expand-row',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
    'diff-hunk',
    'diff-line diff-ctx',
    'diff-line diff-ctx',
  ]);
  const lineRows = kids.filter(r => r.classList.contains('diff-line'));
  assert.deepEqual(lineRows.map(r => r.children[1].textContent), ['1', '2', '3', '4', '5', '6', '7', '8', '9', '10']);
  const inserted = lineRows.slice(4, 8);
  assert.deepEqual(inserted.map(r => r.className), Array.from({ length: 4 }, () => 'diff-line diff-ctx'));
  assert.deepEqual(inserted.map(r => r.children[3].textContent), fetched);
});

test('a failed expansion re-enables the button and reports the error', async () => {
  mockFetch([route('GET', contentPath('&start=21&count=20'), errorResponse('boom', 500))]);
  const { el } = mount([gapFile(41)], []);
  const above = el.querySelectorAll('tr.diff-expand-row')[1];
  const btn = above.querySelector('button');

  btn.click();
  await flush();

  assert.equal(btn.disabled, false);
  assert.equal(btn.textContent, 'Error');
  assert.deepEqual([above.dataset.fromLine, above.dataset.toLine], ['5', '40']);
  assert.equal(el.querySelectorAll('tr.diff-line').length, 6);
});

test('clicking a line opens a comment form and clicking it again closes it', () => {
  const m = mount([makeFile()], []);
  const row = rowFor(m.el, 2);

  row.click();
  const form = m.el.querySelector('tr.comment-form-row');
  assert.ok(form);
  assert.equal(form.previousElementSibling.className, 'diff-line diff-add');
  assert.equal(form.previousElementSibling.dataset.right, '2');
  assert.equal(form.querySelector('textarea').placeholder, 'Leave a comment… (shift-click another line to extend)');
  assert.equal(form.querySelector('textarea').rows, 3);
  assert.deepEqual(m.rctx.commentAnchor, { path: 'a.js', line: 2 });

  row.click();
  assert.equal(m.el.querySelectorAll('tr.comment-form-row').length, 0);
  assert.equal(m.rctx.commentAnchor, null);
});

test('shift-clicking extends the pending comment to a range', () => {
  const m = mount([makeFile()], []);
  rowFor(m.el, 3).click();
  shiftClick(rowFor(m.el, 1));

  const form = m.el.querySelector('tr.comment-form-row');
  assert.ok(form);
  assert.equal(form.previousElementSibling.dataset.right, '3');
  assert.equal(form.querySelector('textarea').placeholder, 'Comment on lines 1–3…');
  assert.deepEqual(m.rctx.commentAnchor, { path: 'a.js', line: 3 });
  assert.deepEqual([...m.el.querySelectorAll('tr.comment-range')].map(r => r.dataset.right), ['1', '2', '3']);
});

test('shift-clicking a line in another file starts a new anchor', () => {
  const other = makeFile({
    path: 'b.js',
    old_path: 'b.js',
    hunks: [{ header: '@@ -10,2 +10,2 @@', start_right: 10, end_right: 12, lines: [ctxLine(10, 10, 'ten'), ctxLine(11, 11, 'eleven')] }],
  });
  const m = mount([makeFile(), other], []);
  rowFor(m.el, 2).click();
  const otherFile = m.el.querySelector('#file-b_js');
  const otherRow = otherFile.querySelector('tr.diff-line[data-right="11"]');
  shiftClick(otherRow);

  const form = otherFile.querySelector('tr.comment-form-row');
  assert.equal(form.previousElementSibling.dataset.file, 'b.js');
  assert.equal(form.previousElementSibling.dataset.right, '11');
  assert.equal(form.querySelector('textarea').placeholder, 'Leave a comment… (shift-click another line to extend)');
  assert.deepEqual(m.rctx.commentAnchor, { path: 'b.js', line: 11 });
  assert.equal(m.el.querySelectorAll('tr.comment-range').length, 0);
  assert.equal(rowFor(m.el, 2).dataset.file, 'a.js');
});

test('cancelling the form clears the pending range', () => {
  const m = mount([makeFile()], []);
  rowFor(m.el, 1).click();
  shiftClick(rowFor(m.el, 3));
  assert.equal(m.el.querySelectorAll('tr.comment-range').length, 3);

  m.el.querySelector('.comment-form-actions .btn').click();
  assert.equal(m.el.querySelectorAll('tr.comment-form-row').length, 0);
  assert.equal(m.el.querySelectorAll('tr.comment-range').length, 0);
  assert.equal(m.rctx.commentAnchor, null);
});

test('submitting a single-line comment posts it and renders the comment row', async () => {
  const created = comment({ id: 42, line_number: 2, body: 'nice work' });
  mockFetch([route('POST', '/api/projects/proj/reviews/7/comments', jsonResponse(created))]);
  const m = mount([makeFile()], []);
  const row = rowFor(m.el, 2);

  row.click();
  m.el.querySelector('textarea').value = 'nice work';
  m.el.querySelector('.btn-primary').click();
  await flush();

  assert.deepEqual(callsFor('POST', '/api/projects/proj/reviews/7/comments')[0].body, {
    body: 'nice work',
    file_path: 'a.js',
    line_number: 2,
    line_end: null,
  });
  assert.equal(m.el.querySelectorAll('tr.comment-form-row').length, 0);
  const rendered = m.el.querySelector('tr.comment-row');
  assert.equal(rendered.dataset.commentId, '42');
  assert.equal(rendered.previousElementSibling.dataset.right, '2');
  assert.equal(rendered.querySelector('.comment-body').textContent, 'nice work');
  assert.equal(m.rctx.commentAnchor, null);
});

test('submitting a range posts the larger line number as line_end', async () => {
  const created = comment({ id: 43, line_number: 1, line_end: 3, body: 'range note' });
  mockFetch([route('POST', '/api/projects/proj/reviews/7/comments', jsonResponse(created))]);
  const m = mount([makeFile()], []);

  rowFor(m.el, 3).click();
  shiftClick(rowFor(m.el, 1));
  m.el.querySelector('textarea').value = 'range note';
  m.el.querySelector('.btn-primary').click();
  await flush();

  assert.deepEqual(callsFor('POST', '/api/projects/proj/reviews/7/comments')[0].body, {
    body: 'range note',
    file_path: 'a.js',
    line_number: 1,
    line_end: 3,
  });
  assert.equal(m.el.querySelectorAll('tr.comment-range').length, 0);
  const rendered = m.el.querySelector('tr.comment-row');
  assert.equal(rendered.previousElementSibling.dataset.right, '3');
  assert.match(rendered.querySelector('.comment-meta').textContent, /^lines 1–3 · /);
});

test('an empty comment body sends no request', async () => {
  mockFetch([route('POST', '/api/projects/proj/reviews/7/comments', jsonResponse(comment({ id: 1 })))]);
  const m = mount([makeFile()], []);
  rowFor(m.el, 2).click();

  m.el.querySelector('.btn-primary').click();
  await flush();
  assert.equal(fetchCalls.length, 0);

  m.el.querySelector('textarea').value = '   ';
  m.el.querySelector('.btn-primary').click();
  await flush();
  assert.equal(fetchCalls.length, 0);
  assert.equal(m.el.querySelectorAll('tr.comment-form-row').length, 1);
});

test('deleting an inline comment removes its row after the request', async () => {
  mockFetch([route('DELETE', '/api/projects/proj/reviews/7/comments/9', { status: 204, body: '', headers: {} })]);
  const m = mount([makeFile()], [comment({ id: 9, line_number: 2 })]);
  assert.equal(m.el.querySelectorAll('tr.comment-row').length, 1);

  m.el.querySelector('.comment-delete').click();
  await flush();

  assert.equal(callsFor('DELETE', '/api/projects/proj/reviews/7/comments/9').length, 1);
  assert.equal(m.el.querySelectorAll('tr.comment-row').length, 0);
  assert.equal(m.el.querySelectorAll('tr.diff-line').length, 5);
});

test('deleting a file-level comment removes its block', async () => {
  mockFetch([route('DELETE', '/api/projects/proj/reviews/7/comments/11', { status: 204, body: '', headers: {} })]);
  const comments = [
    comment({ id: 11, line_number: null, body: 'file note' }),
    comment({ id: 12, line_number: 40, line_end: 42, body: 'on a hidden line' }),
  ];
  const m = mount([makeFile()], comments);
  const blocks = [...m.el.querySelectorAll('.file-level-comment')];
  assert.equal(blocks.length, 2);
  assert.equal(blocks[0].querySelector('.file-level-note').textContent, 'On this file — the line it was written against is gone.');
  assert.equal(blocks[1].querySelector('.file-level-note').textContent, 'On lines 40–42, which this diff does not show.');
  assert.equal(blocks[1].querySelector('.comment-delete').title, 'Delete comment');

  blocks[0].querySelector('.comment-delete').click();
  await flush();

  assert.equal(callsFor('DELETE', '/api/projects/proj/reviews/7/comments/11').length, 1);
  const left = [...m.el.querySelectorAll('.file-level-comment')];
  assert.equal(left.length, 1);
  assert.equal(left[0].querySelector('.comment-body').textContent, 'on a hidden line');
});

test('clicking a line that cannot take comments explains why', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const m = mount([makeFile()], []);
  m.el.querySelector('tr.diff-line.diff-del').click();
  assert.equal(m.el.querySelectorAll('tr.comment-form-row').length, 0);
  assert.match(document.querySelector('.toast').textContent, /Removed lines can't be commented on/);
});

test('clicking a line while comments are off points at the commit selection', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const m = mount([makeFile()], [], { canComment: false });
  rowFor(m.el, 2).click();
  assert.equal(m.el.querySelectorAll('tr.comment-form-row').length, 0);
  assert.match(document.querySelector('.toast').textContent, /commits are selected/);
});
