import test from 'node:test';
import assert from 'node:assert/strict';
import { resetDOM, flush, mockFetch, route, jsonResponse, errorResponse, callsFor, fetchCalls } from './setup.js';
import { formatDate } from '../dom.js';
import {
  renderHome,
  renderProject,
  renderReview,
  reviewSection,
  makeReviewPanel,
  makeCommitList,
  makeGeneralComment,
  relativeTime,
} from '../views.js';

const SLUG = 'demo';
const BRANCH = 'feat/x';
const REV = '/api/projects/demo/reviews/1';
const UPDATED = '2026-01-02 03:04:05';

test.beforeEach(() => resetDOM());

async function settle() {
  await flush();
  await flush();
}

function review(over = {}) {
  return {
    id: 1,
    branch: BRANCH,
    base_branch: 'main',
    title: 'Add widget',
    status: 'open',
    merged: false,
    head_sha: 'head1111',
    base_sha: 'base2222',
    last_reviewed_sha: null,
    updated_at: UPDATED,
    ...over,
  };
}

function parsedFile(path, over = {}) {
  return {
    path,
    old_path: null,
    status: 'M',
    binary: false,
    hunks: [
      {
        header: '@@ -1,3 +1,3 @@',
        start_right: 1,
        end_right: 3,
        lines: [
          { type: 'ctx', content: 'one', left: 1, right: 1 },
          { type: 'add', content: 'two', left: null, right: 2 },
          { type: 'ctx', content: 'three', left: 2, right: 3 },
        ],
      },
    ],
    ...over,
  };
}

function pageRoutes(rev, opts = {}) {
  return [
    route('GET', '/api/projects/' + SLUG + '/reviews?branch=' + encodeURIComponent(rev.branch), jsonResponse([rev])),
    route('GET', '/api/projects/' + SLUG + '/reviews/' + rev.id + '/comments', jsonResponse(opts.comments ?? [])),
    route('GET', '/api/projects/' + SLUG + '/reviews/' + rev.id + '/diff/parsed', jsonResponse(opts.files ?? [parsedFile('src/a.js')])),
    route('GET', '/api/projects/' + SLUG + '/reviews/' + rev.id + '/commits', jsonResponse(opts.commits ?? [])),
  ];
}

async function renderPage(rev, opts = {}) {
  mockFetch([...pageRoutes(rev, opts), ...(opts.extra ?? [])]);
  await renderReview(SLUG, BRANCH);
}

function viewBtn(text) {
  return [...document.querySelectorAll('.view-option-btn')].find((b) => b.textContent === text);
}

test('renderHome lists a project card per project', async () => {
  mockFetch([route('GET', '/api/projects', jsonResponse([
    { slug: 'alpha' },
    { slug: 'beta' },
  ]))]);
  await renderHome();

  const cards = document.querySelectorAll('.project-card');
  assert.equal(cards.length, 2);
  assert.equal(cards[0].getAttribute('href'), '#/projects/alpha');
  assert.equal(cards[0].querySelector('.project-name').textContent, 'alpha');
  assert.equal(cards[1].getAttribute('href'), '#/projects/beta');
  assert.equal(cards[1].querySelector('.project-name').textContent, 'beta');
  assert.equal(document.querySelector('#app h2').textContent, 'Projects');
});

test('renderHome shows the empty state when there are no projects', async () => {
  mockFetch([route('GET', '/api/projects', jsonResponse([]))]);
  await renderHome();

  assert.equal(document.querySelector('#app .muted').textContent, 'No projects. Run: reviewer init');
  assert.equal(document.querySelector('.project-list'), null);
});

test('renderHome renders an error when the request fails', async () => {
  mockFetch([route('GET', '/api/projects', errorResponse('db offline', 500))]);
  await renderHome();

  assert.equal(document.querySelector('#app .error-msg').textContent, 'Error: db offline');
});

const GROUPED = [
  { id: 1, branch: 'open-branch', title: 'Open one', status: 'open', merged: false, updated_at: '' },
  { id: 2, branch: 'changes-branch', title: 'Changes one', status: 'changes_requested', merged: false, updated_at: '' },
  { id: 3, branch: 'paused-branch', title: 'Paused one', status: 'paused', merged: false, updated_at: '' },
  { id: 4, branch: 'approved-branch', title: 'Approved one', status: 'approved', merged: true, updated_at: '' },
  { id: 5, branch: 'closed-branch', title: 'Closed one', status: 'closed', merged: false, updated_at: '' },
];

test('renderProject groups reviews by who is waiting', async () => {
  mockFetch([
    route('GET', '/api/projects/demo', jsonResponse({ slug: 'demo' })),
    route('GET', '/api/projects/demo/reviews', jsonResponse(GROUPED)),
  ]);
  await renderProject('demo');

  const sections = [...document.querySelectorAll('.review-section')];
  assert.deepEqual(
    sections.map((s) => s.querySelector('h3').firstChild.textContent),
    ['Open (1)', 'Awaiting Updates (1)', 'Paused (1)', 'Everything Else (2)'],
  );
  assert.deepEqual(
    sections.map((s) => s.querySelector('.review-section-hint').textContent),
    ['waiting for review', 'waiting on the agent', 'waiting on you', 'approved or closed'],
  );
  assert.deepEqual(
    sections.map((s) => [...s.querySelectorAll('.review-title')].map((t) => t.textContent)),
    [['Open one'], ['Changes one'], ['Paused one'], ['Approved one', 'Closed one']],
  );
  assert.equal(sections[0].querySelector('.review-row').getAttribute('href'), '#/projects/demo/open-branch');
  assert.equal(sections[0].querySelector('.review-subtitle').textContent, 'open-branch · ');
  assert.equal(document.querySelector('#app .back-link').getAttribute('href'), '#/');
});

test('renderProject shows only the sections that have reviews', async () => {
  mockFetch([
    route('GET', '/api/projects/demo', jsonResponse({ slug: 'demo' })),
    route('GET', '/api/projects/demo/reviews', jsonResponse([
      { id: 1, branch: 'open-branch', title: 'Open one', status: 'open', merged: false, updated_at: '' },
      { id: 6, branch: 'open-two', title: 'Open two', status: 'open', merged: false, updated_at: '' },
    ])),
  ]);
  await renderProject('demo');

  const sections = [...document.querySelectorAll('.review-section')];
  assert.equal(sections.length, 1);
  assert.equal(sections[0].querySelector('h3').firstChild.textContent, 'Open (2)');
  assert.equal(sections[0].querySelector('.review-section-hint').textContent, 'waiting for review');
  assert.deepEqual([...document.querySelectorAll('.review-title')].map((t) => t.textContent), ['Open one', 'Open two']);
});

test('renderProject puts the merged badge next to the status badge', async () => {
  mockFetch([
    route('GET', '/api/projects/demo', jsonResponse({ slug: 'demo' })),
    route('GET', '/api/projects/demo/reviews', jsonResponse([
      { id: 4, branch: 'approved-branch', title: 'Approved one', status: 'approved', merged: true, updated_at: '' },
    ])),
  ]);
  await renderProject('demo');

  const badges = [...document.querySelector('.review-row-badges').children];
  assert.deepEqual(badges.map((b) => b.className), ['badge badge-approved', 'badge badge-merged']);
  assert.deepEqual(badges.map((b) => b.textContent), ['approved', 'merged']);
});

test('renderProject shows the push a branch message when there are no reviews', async () => {
  mockFetch([
    route('GET', '/api/projects/demo', jsonResponse({ slug: 'demo' })),
    route('GET', '/api/projects/demo/reviews', jsonResponse([])),
  ]);
  await renderProject('demo');

  assert.equal(document.querySelector('#app .muted').textContent, 'No reviews yet. Push a feature branch to trigger one.');
  assert.equal(document.querySelector('.review-section'), null);
});

test('renderProject renders an error when the project request fails', async () => {
  mockFetch([
    route('GET', '/api/projects/demo', errorResponse('no such project', 404)),
    route('GET', '/api/projects/demo/reviews', jsonResponse([])),
  ]);
  await renderProject('demo');

  assert.equal(document.querySelector('#app .error-msg').textContent, 'Error: no such project');
});

test('reviewSection omits the hint when none is given and falls back to the branch title', () => {
  const r = { id: 9, branch: 'lonely', title: '', status: 'open', updated_at: '' };
  const section = reviewSection('Open', [r, r], 'demo');

  assert.equal(section.querySelector('h3').textContent, 'Open (2)');
  assert.equal(section.querySelector('.review-section-hint'), null);
  assert.equal(section.querySelector('.review-title').textContent, 'lonely');
  assert.equal(section.querySelectorAll('.review-row').length, 2);
});

test('the review header carries the title, badges and branch meta line', async () => {
  const rev = review({ title: 'Add widget', status: 'changes_requested', merged: true });
  await renderPage(rev);

  const header = document.querySelector('.review-header');
  assert.equal(header.querySelector('h2').textContent, 'Add widget');
  assert.deepEqual([...header.querySelectorAll('.badge')].map((b) => b.className), [
    'badge badge-changes_requested',
    'badge badge-merged',
  ]);
  assert.equal(header.querySelector('.review-meta').textContent, 'feat/x → main · updated ' + formatDate(UPDATED));
  assert.equal(document.querySelector('.files-label').textContent, '1 file changed');
  assert.equal(document.querySelector('#app .back-link').getAttribute('href'), '#/projects/demo');
});

test('the review header falls back to the branch name and omits the merged badge', async () => {
  await renderPage(review({ title: null, merged: false }));

  const header = document.querySelector('.review-header');
  assert.equal(header.querySelector('h2').textContent, 'feat/x');
  assert.deepEqual([...header.querySelectorAll('.badge')].map((b) => b.textContent), ['open']);
});

test('renderReview reports a branch that has no review', async () => {
  mockFetch([route('GET', '/api/projects/demo/reviews?branch=feat%2Fx', jsonResponse([]))]);
  await renderReview(SLUG, BRANCH);

  assert.equal(document.querySelector('#app .error-msg').textContent, 'Error: No review found for branch: feat/x');
});

test('a review with no changed files says so and still offers the comment form', async () => {
  await renderPage(review(), { files: [] });

  assert.equal(document.querySelector('.files-label').textContent, '0 files changed');
  assert.equal(document.querySelectorAll('.file-pill').length, 0);
  assert.equal(document.querySelector('.file-pills-toggle'), null);
  assert.equal(document.querySelector('#app p.muted').textContent, 'No changes to display.');
  assert.equal(document.querySelector('.general-comment-form textarea').placeholder, 'Leave a comment…');
});

test('the pill bar lists every file and the overflow toggle expands the capped bar', async () => {
  const files = [];
  for (let i = 0; i < 11; i++) files.push(parsedFile('src/f' + i + '.js'));
  files.push(parsedFile('src/new.js', { old_path: 'src/old.js', status: 'R' }));
  await renderPage(review(), { files });

  assert.equal(document.querySelector('.files-label').textContent, '12 files changed');
  const pills = document.querySelectorAll('.file-pill');
  const pillsBar = document.querySelector('.file-pills');
  assert.equal(pills.length, 12);
  assert.ok(pillsBar.classList.contains('file-pills-capped'));
  assert.equal(pills[0].className, 'file-pill status-m');
  assert.equal(pills[0].textContent, 'src/f0.js');
  assert.equal(pills[11].className, 'file-pill status-r');
  assert.equal(pills[11].textContent, 'src/old.js → src/new.js');

  const toggle = document.querySelector('.file-pills-toggle');
  assert.equal(toggle.textContent, 'Show all 12 files ▾');
  toggle.click();
  assert.ok(pillsBar.classList.contains('expanded'));
  assert.equal(toggle.textContent, 'Show fewer ▴');
  toggle.click();
  assert.equal(pillsBar.classList.contains('expanded'), false);
  assert.equal(toggle.textContent, 'Show all 12 files ▾');
});

test('a file whose old path matches its new path is not shown as a rename', async () => {
  await renderPage(review(), { files: [parsedFile('src/a.js', { old_path: 'src/a.js', status: 'R' })] });

  assert.equal(document.querySelector('.file-pill').textContent, 'src/a.js');
  assert.equal(document.querySelector('.file-pill').className, 'file-pill status-r');
  assert.equal(document.querySelector('.file-tag-renamed'), null);
  assert.equal(document.querySelector('.diff-file-name').textContent, 'src/a.js');
});

test('clicking a pill scrolls to that file block', async () => {
  await renderPage(review(), { files: [parsedFile('src/a.js'), parsedFile('src/b.js')] });

  const scrolled = [];
  Element.prototype.scrollIntoView = function () { scrolled.push(this.id); };
  document.querySelectorAll('.file-pill')[1].click();

  assert.deepEqual(scrolled, ['file-src_b_js']);
  assert.ok(document.getElementById('file-src_b_js'));
});

test('files collapse by default above ten files and deleted files always collapse', async () => {
  const many = [];
  for (let i = 0; i < 11; i++) many.push(parsedFile('src/f' + i + '.js'));
  await renderPage(review(), { files: many });

  const bodies = [...document.querySelectorAll('.diff-file-body')];
  assert.equal(bodies.length, 11);
  assert.equal(bodies.filter((b) => b.classList.contains('hidden')).length, 11);
  assert.deepEqual([...document.querySelectorAll('.file-collapse-btn')].map((b) => b.textContent).slice(0, 3), ['▶', '▶', '▶']);

  mockFetch(pageRoutes(review(), { files: [parsedFile('src/a.js'), parsedFile('src/gone.js', { status: 'D' })] }));
  await renderReview(SLUG, BRANCH);

  const [kept, gone] = [...document.querySelectorAll('.diff-file-body')];
  assert.equal(kept.classList.contains('hidden'), false);
  assert.equal(gone.classList.contains('hidden'), true);
  assert.deepEqual([...document.querySelectorAll('.file-collapse-btn')].map((b) => b.textContent), ['▼', '▶']);
  assert.equal(document.querySelector('.file-tag-deleted').textContent, 'deleted');
  assert.equal(document.querySelectorAll('.file-fullfile-btn').length, 1);
});

test('a file header click toggles that file', async () => {
  await renderPage(review(), { files: [parsedFile('src/a.js'), parsedFile('src/b.js')] });

  const header = document.querySelectorAll('.diff-file-header')[0];
  const body = document.querySelectorAll('.diff-file-body')[0];
  header.click();
  assert.ok(body.classList.contains('hidden'));
  assert.equal(header.querySelector('.file-collapse-btn').textContent, '▶');
  header.click();
  assert.equal(body.classList.contains('hidden'), false);
  assert.equal(header.querySelector('.file-collapse-btn').textContent, '▼');
});

test('comments on files outside the diff land in the general comments section', async () => {
  await renderPage(review(), {
    files: [parsedFile('src/a.js')],
    comments: [
      { id: 5, body: 'stray note', file_path: 'src/other.js', line_number: 4, line_end: null, created_at: UPDATED },
      { id: 6, body: 'no file at all', file_path: null, line_number: null, line_end: null, created_at: UPDATED },
      { id: 7, body: 'inline note', file_path: 'src/a.js', line_number: 2, line_end: null, created_at: UPDATED },
    ],
  });

  const general = [...document.querySelectorAll('.general-comments .general-comment')];
  assert.equal(general.length, 2);
  assert.deepEqual(general.map((g) => g.querySelector('.comment-body').textContent), ['stray note', 'no file at all']);
  assert.equal(general[0].querySelector('.comment-meta').textContent, 'src/other.js:4 · ' + formatDate(UPDATED));
  assert.equal(document.querySelectorAll('tr.comment-row').length, 1);
});

test('posting a general comment appends it and clears the textarea', async () => {
  await renderPage(review(), {
    files: [parsedFile('src/a.js')],
    comments: [{ id: 7, body: 'inline note', file_path: 'src/a.js', line_number: 2, line_end: null, created_at: UPDATED }],
    extra: [route('POST', REV + '/comments', jsonResponse({ id: 99, body: 'brand new', file_path: null, line_number: null, created_at: UPDATED }))],
  });

  const textarea = document.querySelector('.general-comment-form textarea');
  const submit = document.querySelector('.general-comment-form .btn-primary');

  submit.click();
  await settle();
  assert.equal(callsFor('POST', REV + '/comments').length, 0);

  textarea.value = 'brand new';
  submit.click();
  await settle();

  const posts = callsFor('POST', REV + '/comments');
  assert.equal(posts.length, 1);
  assert.deepEqual(posts[0].body, { body: 'brand new' });
  const general = [...document.querySelectorAll('.general-comments .general-comment')];
  assert.equal(general.length, 1);
  assert.equal(general[0].querySelector('.comment-body').textContent, 'brand new');
  assert.equal(textarea.value, '');
});

test('Highlight fetches the highlighted diff once and reuses it on later re-renders', async () => {
  const marked = parsedFile('src/a.js', {
    hunks: [{
      header: '@@ -1,3 +1,3 @@',
      start_right: 1,
      end_right: 3,
      lines: [
        { type: 'ctx', content: 'one', left: 1, right: 1 },
        { type: 'add', content: 'two', left: null, right: 2, html: '<span class="hl">two</span>' },
        { type: 'ctx', content: 'three', left: 2, right: 3 },
      ],
    }],
  });
  await renderPage(review(), {
    files: [parsedFile('src/a.js')],
    extra: [route('GET', REV + '/diff/parsed?highlight=true', jsonResponse([marked]))],
  });

  const hlBtn = viewBtn('Highlight');
  assert.equal(document.querySelector('.diff-line.diff-add .code').textContent, 'two');

  hlBtn.click();
  await settle();
  assert.ok(hlBtn.classList.contains('active'));
  assert.equal(callsFor('GET', REV + '/diff/parsed?highlight=true').length, 1);
  assert.equal(document.querySelector('.diff-line.diff-add .code').innerHTML, '<span class="hl">two</span>');

  viewBtn('Side-by-side').click();
  assert.equal(callsFor('GET', REV + '/diff/parsed?highlight=true').length, 1);
  viewBtn('Side-by-side').click();

  hlBtn.click();
  await settle();
  assert.equal(hlBtn.classList.contains('active'), false);
  assert.equal(document.querySelector('.diff-line.diff-add .code').textContent, 'two');

  hlBtn.click();
  await settle();
  assert.equal(callsFor('GET', REV + '/diff/parsed?highlight=true').length, 1);
  assert.equal(document.querySelector('.diff-line.diff-add .code').innerHTML, '<span class="hl">two</span>');
});

test('Since last review is disabled until a review action has been taken', async () => {
  await renderPage(review({ last_reviewed_sha: null }));

  const btn = viewBtn('Since last review');
  assert.equal(btn.disabled, true);
  assert.equal(btn.title, 'No review action has been taken yet');
});

test('Since last review refetches the scoped diff and toggles back to the full diff', async () => {
  const full = [parsedFile('src/a.js'), parsedFile('src/b.js')];
  const scoped = [parsedFile('src/a.js')];
  await renderPage(review({ last_reviewed_sha: 'sha0001' }), {
    files: full,
    extra: [
      route('GET', REV + '/diff/parsed?highlight=true', jsonResponse(full)),
      route('GET', REV + '/diff/parsed?&since=last_review', jsonResponse(scoped)),
      route('GET', REV + '/diff/parsed?highlight=true&since=last_review', jsonResponse(scoped)),
      route('GET', REV + '/diff/parsed?', jsonResponse(full)),
    ],
  });
  assert.equal(document.querySelector('.files-label').textContent, '2 files changed');

  viewBtn('Highlight').click();
  await settle();

  const sinceBtn = viewBtn('Since last review');
  assert.equal(sinceBtn.disabled, false);
  sinceBtn.click();
  await settle();

  assert.equal(callsFor('GET', REV + '/diff/parsed?&since=last_review').length, 1);
  assert.equal(callsFor('GET', REV + '/diff/parsed?highlight=true&since=last_review').length, 1);
  assert.equal(document.querySelector('.files-label').textContent, '1 file changed');
  assert.equal(document.querySelectorAll('.file-pill').length, 1);
  assert.ok(sinceBtn.classList.contains('active'));
  assert.equal(sinceBtn.disabled, false);

  sinceBtn.click();
  await settle();

  assert.equal(callsFor('GET', REV + '/diff/parsed?').length, 1);
  assert.equal(document.querySelector('.files-label').textContent, '2 files changed');
  assert.equal(sinceBtn.classList.contains('active'), false);
});

test('Side-by-side re-renders the diff and clicking it again returns to unified', async () => {
  await renderPage(review());

  const modeBtn = viewBtn('Side-by-side');
  modeBtn.click();
  assert.ok(modeBtn.classList.contains('active'));
  assert.ok(document.querySelector('.diff-table').classList.contains('diff-sbs'));
  assert.equal(document.querySelectorAll('.diff-line .code-left').length, document.querySelectorAll('tr.diff-line').length);
  const rows = document.querySelectorAll('tr.diff-line');
  assert.equal(rows[1].querySelector('.code-left').textContent, '');
  assert.equal(rows[1].querySelector('.code-right').textContent, 'two');
  assert.equal(rows[1].querySelector('.ln-left').textContent, '');
  assert.equal(rows[0].querySelector('.code-left').textContent, 'one');

  modeBtn.click();
  assert.equal(modeBtn.classList.contains('active'), false);
  assert.equal(document.querySelector('.diff-table').classList.contains('diff-sbs'), false);
  assert.equal(document.querySelector('.code-left'), null);
});

test('Collapse all toggles every file and is disabled with a single file', async () => {
  await renderPage(review(), { files: [parsedFile('src/a.js'), parsedFile('src/b.js')] });

  const collapseAll = viewBtn('Collapse all');
  assert.equal(collapseAll.disabled, false);
  collapseAll.click();

  const bodies = () => [...document.querySelectorAll('.diff-file-body')];
  assert.deepEqual(bodies().map((b) => b.classList.contains('hidden')), [true, true]);
  assert.deepEqual([...document.querySelectorAll('.file-collapse-btn')].map((b) => b.textContent), ['▶', '▶']);
  assert.ok(collapseAll.classList.contains('active'));

  collapseAll.click();
  assert.deepEqual(bodies().map((b) => b.classList.contains('hidden')), [false, false]);
  assert.deepEqual([...document.querySelectorAll('.file-collapse-btn')].map((b) => b.textContent), ['▼', '▼']);
  assert.equal(collapseAll.classList.contains('active'), false);

  await renderPage(review(), { files: [parsedFile('src/a.js')] });
  assert.equal(viewBtn('Collapse all').disabled, true);
});

test('a commit range refetches with from and to, disables commenting, and moves the full-file sha', async () => {
  const commits = [
    { sha: 'c0c0c0c0c0c0c0c0c0c0', message: 'first', body: '', time: 1700000000 },
    { sha: 'd0d0d0d0d0d0d0d0d0d0', message: 'second', body: '', time: 1700003600 },
  ];
  const files = [parsedFile('src/a.js'), parsedFile('src/b.js')];
  const fullFile = jsonResponse({ lines: ['full file line', 'second line'], start: 1 });
  await renderPage(review(), {
    files,
    commits,
    comments: [{ id: 7, body: 'inline note', file_path: 'src/a.js', line_number: 2, line_end: null, created_at: UPDATED }],
    extra: [
      route('GET', REV + '/diff/parsed?&from=base2222&to=c0c0c0c0c0c0c0c0c0c0', jsonResponse(files)),
      route('GET', REV + '/diff/parsed?&from=base2222&to=d0d0d0d0d0d0d0d0d0d0', jsonResponse(files)),
      route('GET', REV + '/diff/parsed?', jsonResponse(files)),
      route('GET', REV + '/file-content?path=src%2Fa.js&sha=d0d0d0d0d0d0d0d0d0d0', fullFile),
      route('GET', REV + '/file-content?path=src%2Fa.js&sha=head1111', fullFile),
    ],
  });
  assert.equal(document.querySelectorAll('tr.comment-row').length, 1);
  assert.equal(document.querySelectorAll('tr.diff-line.diff-line-static').length, 0);

  document.querySelectorAll('.commit-sha')[0].click();
  await settle();
  assert.equal(callsFor('GET', REV + '/diff/parsed?&from=base2222&to=c0c0c0c0c0c0c0c0c0c0').length, 1);

  document.querySelectorAll('.commit-sha')[1].click();
  await settle();
  assert.equal(callsFor('GET', REV + '/diff/parsed?&from=base2222&to=d0d0d0d0d0d0d0d0d0d0').length, 1);

  assert.equal(document.querySelectorAll('tr.diff-line').length, 6);
  assert.equal(document.querySelectorAll('tr.diff-line.diff-line-static').length, 6);
  assert.equal(document.querySelectorAll('tr.comment-row').length, 0);

  document.querySelector('tr.diff-line').click();
  assert.match(document.querySelector('.toast').textContent, /^Comments are off/);
  assert.equal(document.querySelector('tr.comment-form-row'), null);

  document.querySelector('.file-fullfile-btn').click();
  await settle();
  assert.equal(callsFor('GET', REV + '/file-content?path=src%2Fa.js&sha=d0d0d0d0d0d0d0d0d0d0').length, 1);

  document.querySelector('.commit-show-all-btn').click();
  await settle();

  assert.equal(callsFor('GET', REV + '/diff/parsed?').length, 1);
  assert.equal(document.querySelectorAll('tr.diff-line.diff-line-static').length, 0);
  assert.equal(document.querySelectorAll('tr.comment-row').length, 1);
  assert.equal(callsFor('GET', REV + '/file-content?path=src%2Fa.js&sha=head1111').length, 1);
  assert.ok(document.querySelector('.commit-show-all-btn').classList.contains('hidden'));
});

test('an action button posts the textarea comment, patches the status, then navigates', async () => {
  const rev = review();
  const clist = document.createElement('div');
  mockFetch([
    ...pageRoutes(rev),
    route('POST', REV + '/comments', jsonResponse({ id: 42, body: 'Approve\n\nlooks good', file_path: null, line_number: null, created_at: UPDATED })),
    route('PATCH', REV, jsonResponse({ ...rev, status: 'approved' })),
  ]);

  const panel = makeReviewPanel({ commits: [], lastReviewedSHA: null, slug: SLUG, id: 1, rev, clist, baseSha: rev.base_sha, onRange: () => {}, onShowAll: () => {} });
  document.body.appendChild(panel.drawer);
  document.body.appendChild(panel.pill);

  panel.drawer.querySelector('.sidebar-textarea').value = 'looks good';
  const approve = panel.drawer.querySelector('.btn-approve');
  approve.click();
  await settle();

  assert.deepEqual(fetchCalls.filter((c) => c.method !== 'GET').map((c) => c.method), ['POST', 'PATCH']);
  assert.deepEqual(callsFor('POST', REV + '/comments')[0].body, { body: 'Approve\n\nlooks good' });
  assert.deepEqual(callsFor('PATCH', REV)[0].body, { status: 'approved' });
  assert.equal(clist.querySelectorAll('.general-comment').length, 1);
  assert.equal(clist.querySelector('.comment-body').textContent, 'Approve\n\nlooks good');
  assert.equal(approve.disabled, false);
  assert.equal(document.querySelector('#app .review-header h2').textContent, 'Add widget');
});

test('an action with a blank textarea only patches the status', async () => {
  const rev = review();
  const clist = document.createElement('div');
  mockFetch([
    ...pageRoutes(rev),
    route('PATCH', REV, jsonResponse({ ...rev, status: 'changes_requested' })),
  ]);

  const panel = makeReviewPanel({ commits: [], lastReviewedSHA: null, slug: SLUG, id: 1, rev, clist, baseSha: rev.base_sha, onRange: () => {}, onShowAll: () => {} });
  document.body.appendChild(panel.drawer);
  document.body.appendChild(panel.pill);

  panel.drawer.querySelector('.sidebar-textarea').value = '   ';
  panel.drawer.querySelector('.btn-warn').click();
  await settle();

  assert.equal(callsFor('POST', REV + '/comments').length, 0);
  assert.deepEqual(callsFor('PATCH', REV)[0].body, { status: 'changes_requested' });
  assert.equal(clist.querySelectorAll('.general-comment').length, 0);
  assert.equal(document.querySelector('#app .review-header h2').textContent, 'Add widget');
});

test('the Review pane button is disabled for a review that is not open, and pane buttons toggle the drawer', () => {
  const rev = review({ status: 'approved' });
  const clist = document.createElement('div');
  const panel = makeReviewPanel({ commits: [], lastReviewedSHA: null, slug: SLUG, id: 1, rev, clist, baseSha: null, onRange: () => {}, onShowAll: () => {} });
  document.body.appendChild(panel.drawer);
  document.body.appendChild(panel.pill);

  const [commitsBtn, reviewBtn] = panel.pill.querySelectorAll('button');
  assert.equal(commitsBtn.textContent, 'Commits');
  assert.equal(reviewBtn.textContent, 'Review');
  assert.equal(reviewBtn.disabled, true);
  assert.equal(commitsBtn.disabled, false);
  assert.equal(panel.drawer.querySelectorAll('.sidebar-action-btn').length, 0);
  assert.equal(panel.drawer.querySelector('.review-drawer-pane .muted').textContent, 'No commits.');

  commitsBtn.click();
  assert.ok(panel.drawer.classList.contains('open'));
  assert.ok(panel.pill.classList.contains('drawer-open'));
  assert.ok(commitsBtn.classList.contains('active'));
  assert.equal(panel.drawer.querySelector('.review-drawer-title').textContent, 'Commits');
  assert.ok(panel.drawer.querySelectorAll('.review-drawer-pane')[0].classList.contains('active'));

  commitsBtn.click();
  assert.equal(panel.drawer.classList.contains('open'), false);
  assert.equal(panel.pill.classList.contains('drawer-open'), false);
  assert.equal(commitsBtn.classList.contains('active'), false);
  assert.equal(panel.drawer.querySelector('.review-drawer-title').textContent, '');

  commitsBtn.click();
  panel.drawer.querySelector('.review-drawer-head .btn-icon').click();
  assert.equal(panel.drawer.classList.contains('open'), false);
});

test('an open review gets an enabled Review pane holding the action buttons', () => {
  const rev = review();
  const panel = makeReviewPanel({
    commits: [{ sha: 'aaa1111222233334444', message: 'first commit', body: '', time: 1700000000 }],
    lastReviewedSHA: null,
    slug: SLUG,
    id: 1,
    rev,
    clist: document.createElement('div'),
    baseSha: rev.base_sha,
    onRange: () => {},
    onShowAll: () => {},
  });
  document.body.appendChild(panel.drawer);
  document.body.appendChild(panel.pill);

  const [, reviewBtn] = panel.pill.querySelectorAll('button');
  assert.equal(reviewBtn.disabled, false);
  assert.equal(panel.drawer.querySelector('.commit-list-toggle').textContent, '1 commit ▴');
  assert.equal(panel.drawer.querySelectorAll('.sidebar-action-btn').length, 3);

  const commitsBtn = panel.pill.querySelectorAll('button')[0];
  commitsBtn.click();
  assert.equal(panel.drawer.querySelector('.review-drawer-title').textContent, 'Commits');

  reviewBtn.click();
  assert.ok(panel.drawer.classList.contains('open'));
  assert.equal(panel.drawer.querySelector('.review-drawer-title').textContent, 'Review');
  assert.ok(panel.drawer.querySelectorAll('.review-drawer-pane')[1].classList.contains('active'));
  assert.equal(panel.drawer.querySelectorAll('.review-drawer-pane')[0].classList.contains('active'), false);
  assert.equal(panel.drawer.querySelector('.sidebar-textarea').placeholder, 'Leave a comment (optional)…');
});

const COMMITS = [
  { sha: 'aaa1111222233334444', message: 'first commit', body: '', time: 1700000000 },
  { sha: 'bbb1111222233334444', message: 'second commit', body: 'longer body text', time: 1700003600 },
  { sha: 'ccc1111222233334444', message: 'third commit', body: '', time: 1700007200 },
];

function buildCommitList(opts = {}) {
  const calls = { ranges: [], showAll: 0 };
  const ctrl = makeCommitList(
    opts.commits ?? COMMITS,
    opts.lastReviewedSHA ?? null,
    opts.baseSha ?? 'base0000',
    (from, to) => calls.ranges.push([from, to]),
    () => { calls.showAll += 1; },
  );
  document.body.appendChild(ctrl.node);
  return {
    ctrl,
    calls,
    items: [...ctrl.node.querySelectorAll('.commit-item')],
    shas: [...ctrl.node.querySelectorAll('.commit-sha')],
    status: ctrl.node.querySelector('.commit-range-status'),
    showAllBtn: ctrl.node.querySelector('.commit-show-all-btn'),
    list: ctrl.node.querySelector('.commit-list'),
  };
}

test('the first commit click anchors a single commit and the second extends the range', () => {
  const { calls, items, shas, status, showAllBtn } = buildCommitList();

  assert.equal(document.querySelector('.commit-list-toggle').textContent, '3 commits ▴');
  shas[0].click();
  assert.deepEqual(calls.ranges, [['base0000', COMMITS[0].sha]]);
  assert.deepEqual(items.map((i) => i.className), ['commit-item commit-selectable commit-selected-anchor', 'commit-item commit-selectable', 'commit-item commit-selectable']);
  assert.equal(status.textContent, 'aaa1111 — click another to extend · read-only, comments hidden');
  assert.ok(status.classList.contains('commit-range-readonly'));
  assert.equal(showAllBtn.classList.contains('hidden'), false);

  shas[2].click();
  assert.deepEqual(calls.ranges, [['base0000', COMMITS[0].sha], ['base0000', COMMITS[2].sha]]);
  assert.deepEqual(items.map((i) => i.classList.contains('commit-in-range')), [true, true, true]);
  assert.equal(items.some((i) => i.classList.contains('commit-selected-anchor')), false);
  assert.equal(status.textContent, 'aaa1111 → ccc1111 · read-only, comments hidden');
});

test('a range can be extended backwards from the later commit', () => {
  const { calls, shas, status } = buildCommitList();

  shas[2].click();
  assert.deepEqual(calls.ranges, [[COMMITS[1].sha, COMMITS[2].sha]]);
  assert.equal(status.textContent, 'ccc1111 — click another to extend · read-only, comments hidden');

  shas[0].click();
  assert.deepEqual(calls.ranges, [[COMMITS[1].sha, COMMITS[2].sha], ['base0000', COMMITS[2].sha]]);
  assert.equal(status.textContent, 'aaa1111 → ccc1111 · read-only, comments hidden');
});

test('clicking the commit row outside its summary selects the commit', () => {
  const { calls, items, status } = buildCommitList();

  items[1].click();

  assert.deepEqual(calls.ranges, [[COMMITS[0].sha, COMMITS[1].sha]]);
  assert.equal(items[1].classList.contains('commit-selected-anchor'), true);
  assert.equal(status.textContent, 'bbb1111 — click another to extend · read-only, comments hidden');
});

test('clicking the same commit twice leaves just that commit selected', () => {
  const { calls, items, shas, status } = buildCommitList();

  shas[1].click();
  shas[1].click();

  assert.deepEqual(calls.ranges, [[COMMITS[0].sha, COMMITS[1].sha], [COMMITS[0].sha, COMMITS[1].sha]]);
  assert.deepEqual(items.map((i) => i.classList.contains('commit-in-range')), [false, true, false]);
  assert.equal(items[1].classList.contains('commit-selected-anchor'), false);
  assert.equal(status.textContent, 'bbb1111 · read-only, comments hidden');
});

test('Show all clears the selection and notifies the caller', () => {
  const { calls, items, shas, status, showAllBtn } = buildCommitList();

  shas[0].click();
  showAllBtn.click();

  assert.equal(calls.showAll, 1);
  assert.equal(showAllBtn.classList.contains('hidden'), true);
  assert.equal(status.textContent, 'Click a commit to scope the diff');
  assert.equal(status.classList.contains('commit-range-readonly'), false);
  assert.equal(items.some((i) => i.classList.contains('commit-in-range')), false);

  shas[1].click();
  assert.deepEqual(calls.ranges, [['base0000', COMMITS[0].sha], [COMMITS[0].sha, COMMITS[1].sha]]);
});

test('the last reviewed marker follows the matching commit, exactly or by prefix', () => {
  const exact = buildCommitList({ lastReviewedSHA: COMMITS[1].sha });
  const exactKids = [...exact.list.children];
  assert.equal(exactKids.length, 4);
  assert.equal(exactKids[2].className, 'commit-reviewed-marker');
  assert.equal(exactKids[2].textContent, '── last reviewed ──');

  const prefix = buildCommitList({ lastReviewedSHA: COMMITS[1].sha.slice(0, 7) });
  const prefixKids = [...prefix.list.children];
  assert.equal(prefixKids.length, 4);
  assert.equal(prefixKids[2].className, 'commit-reviewed-marker');

  const last = buildCommitList({ lastReviewedSHA: COMMITS[2].sha });
  assert.equal([...last.list.children].pop().className, 'commit-reviewed-marker');

  const none = buildCommitList({ lastReviewedSHA: 'ffffffffffffffffffff' });
  assert.equal(none.list.querySelectorAll('.commit-reviewed-marker').length, 0);
  assert.equal(none.list.children.length, 3);

  const abbreviated = buildCommitList({
    commits: [{ sha: 'aaa1111', message: 'abbreviated', body: '', time: 1700000000 }],
    lastReviewedSHA: COMMITS[0].sha,
  });
  assert.equal([...abbreviated.list.children].pop().className, 'commit-reviewed-marker');
});

test('clicking a commit summary expands its body without selecting the commit', () => {
  const { calls, items, status, ctrl } = buildCommitList();

  const summary = ctrl.node.querySelectorAll('.commit-summary')[1];
  const body = ctrl.node.querySelector('.commit-body');
  assert.equal(ctrl.node.querySelectorAll('.commit-body').length, 1);
  assert.equal(ctrl.node.querySelectorAll('.commit-summary-expandable').length, 1);
  assert.ok(summary.classList.contains('commit-summary-expandable'));
  assert.equal(body.textContent, 'longer body text');
  assert.ok(body.classList.contains('hidden'));

  summary.click();
  assert.equal(body.classList.contains('hidden'), false);
  assert.ok(summary.classList.contains('commit-summary-open'));
  assert.deepEqual(calls.ranges, []);
  assert.equal(items.some((i) => i.classList.contains('commit-in-range')), false);
  assert.equal(status.textContent, 'Click a commit to scope the diff');

  summary.click();
  assert.ok(body.classList.contains('hidden'));
  assert.equal(summary.classList.contains('commit-summary-open'), false);
});

test('the commit list toggle hides and restores the list', () => {
  const { list, ctrl } = buildCommitList();
  const toggle = ctrl.node.querySelector('.commit-list-toggle');

  toggle.click();
  assert.ok(list.classList.contains('hidden'));
  assert.equal(toggle.textContent, '3 commits ▾');

  toggle.click();
  assert.equal(list.classList.contains('hidden'), false);
  assert.equal(toggle.textContent, '3 commits ▴');
});

test('commit rows show the short sha, message and relative time', () => {
  const { items } = buildCommitList();

  assert.equal(items[0].querySelector('.commit-sha').textContent, 'aaa1111');
  assert.equal(items[0].querySelector('.commit-message').textContent, 'first commit');
  assert.match(items[0].querySelector('.commit-time').textContent, /ago$/);

  const noTime = buildCommitList({ commits: [{ sha: 'ddd1111222233334444', message: 'timeless', body: '', time: 0 }] });
  assert.equal(noTime.items[0].querySelector('.commit-time').textContent, '');
  assert.equal(noTime.ctrl.node.querySelector('.commit-list-toggle').textContent, '1 commit ▴');
});

test('relativeTime names every unit of elapsed time', () => {
  const cases = [
    [0, 'just now'],
    [30, 'just now'],
    [60, '1m ago'],
    [3540, '59m ago'],
    [3600, '1h ago'],
    [82800, '23h ago'],
    [86400, '1d ago'],
    [2505600, '29d ago'],
    [2592000, '1mo ago'],
    [30240000, '11mo ago'],
    [31536000, '1y ago'],
    [63072000, '2y ago'],
  ];

  for (const [ago, expected] of cases) {
    const now = Math.floor(Date.now() / 1000);
    assert.equal(relativeTime(now - ago), expected, 'ago=' + ago + 's');
  }
});

test('makeGeneralComment names the file and line it belongs to', () => {
  const withLine = makeGeneralComment({ id: 1, body: 'first', file_path: 'src/a.js', line_number: 12, line_end: null, created_at: UPDATED }, SLUG, 7);
  assert.equal(withLine.querySelector('.comment-meta').textContent, 'src/a.js:12 · ' + formatDate(UPDATED));

  const withFile = makeGeneralComment({ id: 2, body: 'second', file_path: 'src/a.js', line_number: null, line_end: null, created_at: UPDATED }, SLUG, 7);
  assert.equal(withFile.querySelector('.comment-meta').textContent, 'src/a.js · ' + formatDate(UPDATED));
  assert.equal(withFile.querySelector('.comment-body').textContent, 'second');

  const bare = makeGeneralComment({ id: 3, body: 'third', file_path: null, line_number: null, line_end: null, created_at: UPDATED }, SLUG, 7);
  assert.equal(bare.querySelector('.comment-meta').textContent, formatDate(UPDATED));
  assert.equal(bare.className, 'general-comment');
  assert.equal(bare.querySelector('.comment-delete').title, 'Delete');
});

test('the pill overflow toggle appears only above eight files', async () => {
  const at = [];
  for (let i = 0; i < 8; i++) at.push(parsedFile('src/f' + i + '.js'));
  await renderPage(review(), { files: at });

  assert.equal(document.querySelector('.file-pills').classList.contains('file-pills-capped'), false);
  assert.equal(document.querySelector('.file-pills-toggle'), null);
  assert.equal(document.querySelectorAll('.file-pill').length, 8);

  const over = [];
  for (let i = 0; i < 9; i++) over.push(parsedFile('src/f' + i + '.js'));
  await renderPage(review(), { files: over });

  assert.equal(document.querySelector('.file-pills').classList.contains('file-pills-capped'), true);
  assert.equal(document.querySelector('.file-pills-toggle').textContent, 'Show all 9 files ▾');
});

test('file bodies start collapsed only above ten files', async () => {
  const ten = [];
  for (let i = 0; i < 10; i++) ten.push(parsedFile('src/f' + i + '.js'));
  await renderPage(review(), { files: ten });

  assert.equal(document.querySelectorAll('.diff-file-body').length, 10);
  assert.equal(document.querySelectorAll('.diff-file-body.hidden').length, 0);

  const eleven = [];
  for (let i = 0; i < 11; i++) eleven.push(parsedFile('src/f' + i + '.js'));
  await renderPage(review(), { files: eleven });

  assert.equal(document.querySelectorAll('.diff-file-body').length, 11);
  assert.equal(document.querySelectorAll('.diff-file-body.hidden').length, 11);
});

test('deleting a general comment issues a DELETE and removes it from the page', async () => {
  mockFetch([route('DELETE', '/api/projects/demo/reviews/7/comments/31', { status: 204, body: '', headers: {} })]);
  const node = makeGeneralComment({ id: 31, body: 'gone soon', file_path: null, line_number: null, line_end: null, created_at: UPDATED }, SLUG, 7);
  const host = document.createElement('div');
  host.appendChild(node);
  document.body.appendChild(host);

  node.querySelector('.comment-delete').click();
  await settle();

  assert.equal(callsFor('DELETE', '/api/projects/demo/reviews/7/comments/31').length, 1);
  assert.equal(document.querySelector('.general-comment'), null);
  assert.equal(host.childNodes.length, 0);
});
