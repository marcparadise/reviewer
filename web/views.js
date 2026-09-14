import { api } from './api.js';
import { addRoute, navigate } from './router.js';
import { el, setApp, formatDate, statusBadge, mergedBadge, errMsg, btn, initTheme } from './dom.js';
import { renderDiffView } from './diff.js';

initTheme();

addRoute('/', renderHome);
addRoute('/projects/([^/]+)', renderProject);
addRoute('/projects/([^/]+)/(.+)', renderReview);


export async function renderHome() {
  const container = el('div', { class: 'container' });
  const h2 = document.createElement('h2');
  h2.textContent = 'Projects';
  container.appendChild(h2);
  setApp(container);

  try {
    const projects = await api('GET', '/api/projects');
    if (!projects.length) {
      const p = el('p', { class: 'muted' });
      p.textContent = 'No projects. Run: reviewer init';
      container.appendChild(p);
      return;
    }
    const list = el('div', { class: 'project-list' });
    for (const p of projects) {
      const card = el('a', { class: 'project-card', href: '#/projects/' + p.slug });
      const name = el('div', { class: 'project-name' });
      name.textContent = p.slug;
      card.appendChild(name);
      list.appendChild(card);
    }
    container.appendChild(list);
  } catch (e) {
    container.appendChild(errMsg(e));
  }
}

export async function renderProject(slug) {
  const container = el('div', { class: 'container' });
  const back = el('a', { class: 'back-link', href: '#/' });
  back.textContent = '← Projects';
  container.appendChild(back);
  setApp(container);

  try {
    const [proj, reviews] = await Promise.all([
      api('GET', '/api/projects/' + slug),
      api('GET', '/api/projects/' + slug + '/reviews'),
    ]);

    const h2 = document.createElement('h2');
    h2.textContent = proj.slug;
    container.appendChild(h2);

    if (!reviews.length) {
      const p = el('p', { class: 'muted' });
      p.textContent = 'No reviews yet. Push a feature branch to trigger one.';
      container.appendChild(p);
      return;
    }

    // Group by who the review is waiting on, so it's obvious at a glance what
    // needs a reviewer's attention vs. the agent's vs. nothing. Anything that
    // matches none of the explicit groups (approved, closed, unknown) falls
    // through to "Everything Else".
    const groups = [
      { title: 'Open', hint: 'waiting for review', match: s => s === 'open' },
      { title: 'Awaiting Updates', hint: 'waiting on the agent', match: s => s === 'changes_requested' },
      { title: 'Paused', hint: 'waiting on you', match: s => s === 'paused' },
    ];
    const shown = new Set();
    for (const g of groups) {
      const rs = reviews.filter(r => g.match(r.status));
      rs.forEach(r => shown.add(r));
      if (rs.length) container.appendChild(reviewSection(g.title, rs, slug, g.hint));
    }
    const rest = reviews.filter(r => !shown.has(r));
    if (rest.length) container.appendChild(reviewSection('Everything Else', rest, slug, 'approved or closed'));
  } catch (e) {
    container.appendChild(errMsg(e));
  }
}

export function reviewSection(title, reviews, slug, hint) {
  const section = el('div', { class: 'review-section' });
  const h3 = document.createElement('h3');
  h3.textContent = title + ' (' + reviews.length + ')';
  if (hint) {
    const hintEl = el('span', { class: 'review-section-hint' });
    hintEl.textContent = hint;
    h3.appendChild(hintEl);
  }
  section.appendChild(h3);

  const list = el('div', { class: 'review-list' });
  for (const r of reviews) {
    const row = el('a', { class: 'review-row', href: '#/projects/' + slug + '/' + r.branch });
    const left = el('div', { class: 'review-row-left' });
    const titleEl = el('div', { class: 'review-title' });
    titleEl.textContent = r.title || r.branch;
    const sub = el('div', { class: 'review-subtitle' });
    sub.textContent = r.branch + ' · ' + formatDate(r.updated_at);
    left.appendChild(titleEl);
    left.appendChild(sub);
    row.appendChild(left);
    // Group badges so the row's space-between layout keeps them together on the
    // right instead of spreading status and merged apart.
    const badges = el('div', { class: 'review-row-badges' });
    badges.appendChild(statusBadge(r.status));
    if (r.merged) badges.appendChild(mergedBadge());
    row.appendChild(badges);
    list.appendChild(row);
  }
  section.appendChild(list);
  return section;
}

export function makeReviewPanel({ commits, lastReviewedSHA, slug, id, rev, clist, baseSha, onRange, onShowAll }) {
  let openPane = null;

  // ── Drawer ──
  const drawer = el('div', { class: 'review-drawer' });

  const drawerHead = el('div', { class: 'review-drawer-head' });
  const drawerTitle = el('span', { class: 'review-drawer-title' });
  const closeBtn = el('button', { class: 'btn-icon', title: 'Close' });
  closeBtn.textContent = '✕';
  closeBtn.addEventListener('click', () => switchPane(null));
  drawerHead.appendChild(drawerTitle);
  drawerHead.appendChild(closeBtn);
  drawer.appendChild(drawerHead);

  // ── Commits pane ──
  const commitsPane = el('div', { class: 'review-drawer-pane' });
  let listCtrl = { clearSelection: () => {} };
  if (commits.length) {
    const result = makeCommitList(commits, lastReviewedSHA, baseSha, onRange, onShowAll);
    commitsPane.appendChild(result.node);
    listCtrl = result;
  } else {
    const empty = el('p', { class: 'muted' }); empty.textContent = 'No commits.';
    commitsPane.appendChild(empty);
  }
  drawer.appendChild(commitsPane);

  // ── Review pane ──
  const reviewPane = el('div', { class: 'review-drawer-pane' });
  const textarea = el('textarea', { class: 'sidebar-textarea' });
  textarea.placeholder = 'Leave a comment (optional)…';
  textarea.rows = 5;
  reviewPane.appendChild(textarea);

  if (rev.status === 'open') {
    const dest = '#/projects/' + slug + '/' + rev.branch;
    const actionRow = el('div', { class: 'sidebar-review-actions' });
    const makeActionBtn = (char, label, cls, newStatus) => {
      const b = el('button', { class: 'btn ' + cls + ' sidebar-action-btn', title: label });
      b.textContent = char;
      b.addEventListener('click', async () => {
        b.disabled = true;
        try {
          const userText = textarea.value.trim();
          const body = userText ? label + '\n\n' + userText : null;
          if (body) {
            const c = await api('POST', '/api/projects/' + slug + '/reviews/' + id + '/comments', { body });
            clist.appendChild(makeGeneralComment(c, slug, id));
          }
          await api('PATCH', '/api/projects/' + slug + '/reviews/' + id, { status: newStatus });
          navigate(dest);
        } finally { b.disabled = false; }
      });
      return b;
    };
    actionRow.appendChild(makeActionBtn('✓', 'Approve', 'btn-approve', 'approved'));
    actionRow.appendChild(makeActionBtn('±', 'Request Changes', 'btn-warn', 'changes_requested'));
    actionRow.appendChild(makeActionBtn('⏸', 'Pause', 'btn-paused', 'paused'));
    reviewPane.appendChild(actionRow);
  }
  drawer.appendChild(reviewPane);

  // ── Pill ──
  const pill = el('div', { class: 'review-pill' });
  const commitsBtn = el('button', { class: 'view-option-btn' });
  commitsBtn.textContent = 'Commits';
  const reviewBtn = el('button', { class: 'view-option-btn' });
  reviewBtn.textContent = 'Review';
  if (rev.status !== 'open') reviewBtn.disabled = true;
  pill.appendChild(commitsBtn);
  pill.appendChild(reviewBtn);

  function switchPane(which) {
    openPane = (openPane === which) ? null : which;
    const isOpen = openPane !== null;
    drawer.classList.toggle('open', isOpen);
    pill.classList.toggle('drawer-open', isOpen);
    commitsBtn.classList.toggle('active', openPane === 'commits');
    reviewBtn.classList.toggle('active', openPane === 'review');
    drawerTitle.textContent = openPane === 'commits' ? 'Commits' : openPane === 'review' ? 'Review' : '';
    commitsPane.classList.toggle('active', openPane === 'commits');
    reviewPane.classList.toggle('active', openPane === 'review');
  }

  commitsBtn.addEventListener('click', () => switchPane('commits'));
  reviewBtn.addEventListener('click', () => switchPane('review'));

  return { pill, drawer, listCtrl };
}

export async function renderReview(slug, branch) {
  let diffMode = 'unified';
  // scope: { mode: 'all' } | { mode: 'since' } | { mode: 'range', from: sha, to: sha }
  let scope = { mode: 'all' };
  let highlightedFiles = null;
  let highlightEnabled = false;
  const diffState = { el: null };

  const container = el('div', { class: 'container' });
  const back = el('a', { class: 'back-link', href: '#/projects/' + slug });
  back.textContent = '← Reviews';
  container.appendChild(back);
  setApp(container);

  function scopeParam(s) {
    if (s.mode === 'since') return '&since=last_review';
    if (s.mode === 'range') return '&from=' + encodeURIComponent(s.from) + '&to=' + encodeURIComponent(s.to);
    return '';
  }

  const PILL_CAP_THRESHOLD = 8; // show overflow toggle above this count

  function buildFilesEl(files) {
    const filesEl = el('div', { class: 'files-changed' });
    const label = el('div', { class: 'files-label' });
    label.textContent = files.length + ' file' + (files.length !== 1 ? 's' : '') + ' changed';
    filesEl.appendChild(label);
    const pills = el('div', { class: 'file-pills' + (files.length > PILL_CAP_THRESHOLD ? ' file-pills-capped' : '') });
    for (const f of files) {
      const pill = el('a', {
        class: 'file-pill status-' + f.status.toLowerCase().charAt(0),
        href: '#',
      });
      // Renames carry old_path; show "old → new". The scroll target is always
      // the new path, which is the id the diff view builds its file block from.
      const isRename = f.old_path && f.old_path !== f.path;
      pill.textContent = isRename ? f.old_path + ' → ' + f.path : f.path;
      pill.addEventListener('click', (e) => {
        e.preventDefault();
        const safeId = 'file-' + f.path.replace(/[^a-zA-Z0-9]/g, '_');
        document.getElementById(safeId)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
      });
      pills.appendChild(pill);
    }
    filesEl.appendChild(pills);
    if (files.length > PILL_CAP_THRESHOLD) {
      const tog = el('button', { class: 'file-pills-toggle' });
      tog.textContent = 'Show all ' + files.length + ' files ▾';
      tog.addEventListener('click', () => {
        const expanded = pills.classList.toggle('expanded');
        tog.textContent = expanded ? 'Show fewer ▴' : 'Show all ' + files.length + ' files ▾';
      });
      filesEl.appendChild(tog);
    }
    return filesEl;
  }

  try {
    const reviews = await api('GET', '/api/projects/' + slug + '/reviews?branch=' + encodeURIComponent(branch));
    if (!reviews.length) throw new Error('No review found for branch: ' + branch);
    const rev = reviews[0];
    const id = rev.id;

    const [comments, parsedFiles, commits] = await Promise.all([
      api('GET', '/api/projects/' + slug + '/reviews/' + id + '/comments'),
      api('GET', '/api/projects/' + slug + '/reviews/' + id + '/diff/parsed'),
      api('GET', '/api/projects/' + slug + '/reviews/' + id + '/commits'),
    ]);
    let parsedFilesForScope = parsedFiles;

    // Header
    const header = el('div', { class: 'review-header' });
    const titleRow = el('div', { class: 'review-header-title' });
    const h2 = document.createElement('h2');
    h2.textContent = rev.title || rev.branch;
    titleRow.appendChild(h2);
    titleRow.appendChild(statusBadge(rev.status));
    if (rev.merged) titleRow.appendChild(mergedBadge());
    header.appendChild(titleRow);

    const meta = el('div', { class: 'review-meta' });
    meta.textContent = rev.branch + ' → ' + rev.base_branch + ' · updated ' + formatDate(rev.updated_at);
    header.appendChild(meta);

    container.appendChild(header);

    // Files changed bar
    let filesEl = buildFilesEl(parsedFiles);
    container.appendChild(filesEl);

    // Diff
    const fullFileMode = new Map(); // filePath -> bool
    const collapseMap = new Map();  // filePath -> bool (true = collapsed)
    const COLLAPSE_THRESHOLD = 10;

    function initCollapseMap(files) {
      collapseMap.clear();
      const startCollapsed = files.length > COLLAPSE_THRESHOLD;
      for (const f of files) {
        const path = f.path || f.old_path;
        // Deleted files collapse to just their header indicator by default.
        collapseMap.set(path, startCollapsed || f.status === 'D');
      }
    }
    initCollapseMap(parsedFiles);

    async function applyScope() {
      highlightedFiles = null;
      const sp = scopeParam(scope);
      const newParsed = await api('GET', '/api/projects/' + slug + '/reviews/' + id + '/diff/parsed?' + sp);
      const newFilesEl = buildFilesEl(newParsed);
      filesEl.replaceWith(newFilesEl);
      filesEl = newFilesEl;
      parsedFilesForScope = newParsed;
      initCollapseMap(newParsed);
      // In range mode, full-file and expand-context must fetch from the range's head.
      diffCtx.headSha = scope.mode === 'range' ? scope.to : rev.head_sha;
      diffCtx.canComment = scope.mode !== 'range';
      if (highlightEnabled) {
        highlightedFiles = await api('GET', '/api/projects/' + slug + '/reviews/' + id +
          '/diff/parsed?highlight=true' + sp);
      }
      rerenderDiff();
    }

    const hasLastReview = !!rev.last_reviewed_sha;

    const diffCtx = { slug, reviewId: id, headSha: rev.head_sha, baseSha: rev.base_sha, canComment: true };

    if (parsedFiles.length > 0) {
      diffState.el = renderDiffView(parsedFiles, comments, diffCtx, fullFileMode, collapseMap, diffMode, diffState);
      container.appendChild(diffState.el);
    } else {
      diffState.el = el('p', { class: 'muted' });
      diffState.el.textContent = 'No changes to display.';
      container.appendChild(diffState.el);
    }

    // General comments + standalone comment form
    const renderedPaths = new Set(parsedFiles.map(f => f.path || f.old_path));
    const general = comments.filter(c => !c.file_path || !renderedPaths.has(c.file_path));
    const commentSection = el('div', { class: 'general-comments' });
    const ch3 = document.createElement('h3');
    ch3.textContent = 'Comments';
    commentSection.appendChild(ch3);
    const clist = el('div', { class: 'comment-list' });
    for (const c of general) clist.appendChild(makeGeneralComment(c, slug, id));
    commentSection.appendChild(clist);

    const commentForm = el('div', { class: 'general-comment-form' });
    const commentTextarea = el('textarea', { class: 'form-textarea', rows: '3', placeholder: 'Leave a comment…' });
    const commentSubmitBtn = btn('Comment', 'btn-primary btn-sm', async () => {
      const body = commentTextarea.value.trim();
      if (!body) return;
      const c = await api('POST', '/api/projects/' + slug + '/reviews/' + id + '/comments', { body });
      clist.appendChild(makeGeneralComment(c, slug, id));
      commentTextarea.value = '';
    });
    commentForm.appendChild(commentTextarea);
    const commentFormActions = el('div', { class: 'comment-form-actions' });
    commentFormActions.appendChild(commentSubmitBtn);
    commentForm.appendChild(commentFormActions);
    commentSection.appendChild(commentForm);
    container.appendChild(commentSection);

    function rerenderDiff() {
      diffCtx.highlightEnabled = highlightEnabled;
      const files = (highlightEnabled && highlightedFiles) ? highlightedFiles : parsedFilesForScope;
      const fresh = renderDiffView(files, comments, diffCtx, fullFileMode, collapseMap, diffMode, diffState);
      diffState.el.replaceWith(fresh);
      diffState.el = fresh;
    }

    // View control buttons — shown in the View options drawer pane with text labels.
    const modeBtn = el('button', { class: 'view-option-btn' });
    modeBtn.textContent = 'Side-by-side';
    modeBtn.addEventListener('click', () => {
      diffMode = diffMode === 'unified' ? 'side-by-side' : 'unified';
      modeBtn.classList.toggle('active', diffMode === 'side-by-side');
      rerenderDiff();
    });

    const hlBtn = el('button', { class: 'view-option-btn' });
    hlBtn.textContent = 'Highlight';
    hlBtn.addEventListener('click', async () => {
      hlBtn.disabled = true;
      try {
        highlightEnabled = !highlightEnabled;
        hlBtn.classList.toggle('active', highlightEnabled);
        if (highlightEnabled && !highlightedFiles) {
          highlightedFiles = await api('GET', '/api/projects/' + slug + '/reviews/' + id + '/diff/parsed?highlight=true' + scopeParam(scope));
        }
        rerenderDiff();
      } finally { hlBtn.disabled = false; }
    });

    const scopeBtn = el('button', { class: 'view-option-btn' });
    scopeBtn.textContent = 'Since last review';
    if (!hasLastReview) { scopeBtn.disabled = true; scopeBtn.title = 'No review action has been taken yet'; }
    scopeBtn.addEventListener('click', async () => {
      scopeBtn.disabled = true;
      try {
        await setScope(scope.mode === 'since' ? { mode: 'all' } : { mode: 'since' });
      } finally { if (hasLastReview) scopeBtn.disabled = false; }
    });

    const collapseAllBtn = el('button', { class: 'view-option-btn' });
    collapseAllBtn.textContent = 'Collapse all';
    if (parsedFiles.length <= 1) collapseAllBtn.disabled = true;
    collapseAllBtn.addEventListener('click', () => {
      const anyExpanded = [...collapseMap.values()].some(v => !v);
      const next = anyExpanded;
      for (const key of collapseMap.keys()) collapseMap.set(key, next);
      collapseAllBtn.classList.toggle('active', next);
      if (diffState.el) {
        diffState.el.querySelectorAll('.diff-file-body').forEach(body => body.classList.toggle('hidden', next));
        diffState.el.querySelectorAll('.file-collapse-btn').forEach(b => { b.textContent = next ? '▶' : '▼'; });
      }
    });

    // setScope closes over scopeBtn and commitListCtrl (late-bound below).
    let commitListCtrl = { clearSelection: () => {} };
    async function setScope(next) {
      scope = next;
      scopeBtn.classList.toggle('active', scope.mode === 'since');
      if (scope.mode !== 'range') commitListCtrl.clearSelection();
      await applyScope();
    }

    const { pill, drawer, listCtrl } = makeReviewPanel({
      commits, lastReviewedSHA: rev.last_reviewed_sha, slug, id, rev, clist,
      baseSha: rev.base_sha,
      onRange: (from, to) => setScope({ mode: 'range', from, to }),
      onShowAll: () => setScope({ mode: 'all' }),
    });
    commitListCtrl = listCtrl;

    // Prepend view controls as text buttons at the top of the pill, above a separator.
    const pillSep = el('hr', { class: 'pill-sep' });
    [pillSep, collapseAllBtn, scopeBtn, hlBtn, modeBtn].forEach(b => pill.insertBefore(b, pill.firstChild));

    container.appendChild(pill);
    container.appendChild(drawer);

  } catch (e) {
    container.appendChild(errMsg(e));
  }
}

export function relativeTime(unixSec) {
  const diff = Math.floor(Date.now() / 1000) - unixSec;
  if (diff < 60) return 'just now';
  if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
  if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
  if (diff < 86400 * 30) return Math.floor(diff / 86400) + 'd ago';
  if (diff < 86400 * 365) return Math.floor(diff / (86400 * 30)) + 'mo ago';
  return Math.floor(diff / (86400 * 365)) + 'y ago';
}

// makeCommitList renders the commit list with two-click range selection.
// Returns { node, clearSelection } where clearSelection() removes all highlights
// without triggering any callback (used when scope changes externally).
export function makeCommitList(commits, lastReviewedSHA, baseSha, onRange, onShowAll) {
  const wrap = el('div', { class: 'commit-list-disclosure' });
  const label = commits.length + ' commit' + (commits.length !== 1 ? 's' : '');

  // Status line: shows current selection or "Click a commit to scope the diff"
  const statusLine = el('div', { class: 'commit-range-status' });
  statusLine.textContent = 'Click a commit to scope the diff';

  const showAllBtn = el('button', { class: 'commit-show-all-btn hidden' });
  showAllBtn.textContent = 'Show all';
  showAllBtn.addEventListener('click', () => {
    clearSelection();
    onShowAll();
  });

  const toggle = el('button', { class: 'commit-list-toggle' });
  toggle.textContent = label + ' ▴';
  const list = el('ol', { class: 'commit-list' });
  const items = [];

  // anchorIdx: index into commits[] of first-clicked commit; null = no pending anchor
  let anchorIdx = null;

  function parentSha(i) {
    return i === 0 ? baseSha : commits[i - 1].sha;
  }

  function applyHighlight(lo, hi, isAnchorPending) {
    items.forEach((item, i) => {
      item.classList.remove('commit-selected-anchor', 'commit-in-range');
      if (i >= lo && i <= hi) {
        item.classList.add(isAnchorPending && i === lo ? 'commit-selected-anchor' : 'commit-in-range');
      }
    });
  }

  function clearSelection() {
    anchorIdx = null;
    items.forEach(item => item.classList.remove('commit-selected-anchor', 'commit-in-range'));
    statusLine.textContent = 'Click a commit to scope the diff';
    statusLine.classList.remove('commit-range-readonly');
    showAllBtn.classList.add('hidden');
  }

  function onCommitClick(i) {
    if (anchorIdx === null) {
      // First click: anchor this commit, show single-commit diff
      anchorIdx = i;
      applyHighlight(i, i, true);
      const shortSha = commits[i].sha.slice(0, 7);
      statusLine.textContent = shortSha + ' — click another to extend · read-only, comments hidden';
      statusLine.classList.add('commit-range-readonly');
      showAllBtn.classList.remove('hidden');
      onRange(parentSha(i), commits[i].sha);
    } else {
      // Second click: confirm range (or reset if same commit)
      const lo = Math.min(anchorIdx, i);
      const hi = Math.max(anchorIdx, i);
      anchorIdx = null;
      applyHighlight(lo, hi, false);
      const fromShort = commits[lo].sha.slice(0, 7);
      const toShort = commits[hi].sha.slice(0, 7);
      statusLine.textContent = fromShort + (lo === hi ? '' : ' → ' + toShort) + ' · read-only, comments hidden';
      statusLine.classList.add('commit-range-readonly');
      showAllBtn.classList.remove('hidden');
      onRange(parentSha(lo), commits[hi].sha);
    }
  }

  // Commits arrive oldest-first; marker goes after the last-reviewed commit
  let markerInserted = false;
  for (let idx = 0; idx < commits.length; idx++) {
    const c = commits[idx];
    const item = el('li', { class: 'commit-item commit-selectable' });
    const summary = el('div', { class: 'commit-summary' });
    const sha = el('span', { class: 'commit-sha' });
    sha.textContent = c.sha.slice(0, 7);
    const msg = el('span', { class: 'commit-message' });
    msg.textContent = c.message;
    const time = el('span', { class: 'commit-time' });
    if (c.time) time.textContent = relativeTime(c.time);
    summary.appendChild(sha);
    summary.appendChild(msg);
    summary.appendChild(time);
    item.appendChild(summary);

    if (c.body) {
      const body = el('pre', { class: 'commit-body hidden' });
      body.textContent = c.body;
      item.appendChild(body);
      // Body expansion toggles on summary click; sha/sha click handles selection below.
      summary.classList.add('commit-summary-expandable');
      summary.addEventListener('click', () => {
        body.classList.toggle('hidden');
        summary.classList.toggle('commit-summary-open');
      });
    }

    // Selection on SHA span click so body-expansion click doesn't double-fire.
    sha.addEventListener('click', (e) => {
      e.stopPropagation();
      onCommitClick(idx);
    });
    // Also allow clicking the item row itself (outside the summary) for selection.
    item.addEventListener('click', (e) => {
      if (e.target.closest('.commit-summary')) return;
      onCommitClick(idx);
    });

    items.push(item);
    list.appendChild(item);

    if (!markerInserted && lastReviewedSHA && (c.sha === lastReviewedSHA || c.sha.startsWith(lastReviewedSHA) || lastReviewedSHA.startsWith(c.sha))) {
      const marker = el('li', { class: 'commit-reviewed-marker' });
      marker.textContent = '── last reviewed ──';
      list.appendChild(marker);
      markerInserted = true;
    }
  }

  toggle.addEventListener('click', () => {
    const nowHidden = list.classList.toggle('hidden');
    toggle.textContent = label + (nowHidden ? ' ▾' : ' ▴');
  });

  const statusRow = el('div', { class: 'commit-range-statusrow' });
  statusRow.appendChild(statusLine);
  statusRow.appendChild(showAllBtn);
  wrap.appendChild(toggle);
  wrap.appendChild(statusRow);
  wrap.appendChild(list);
  return { node: wrap, clearSelection };
}

export function makeGeneralComment(c, slug, reviewId) {
  const div = el('div', { class: 'general-comment' });
  const meta = el('div', { class: 'comment-meta' });
  const where = c.file_path && c.line_number != null
    ? c.file_path + ':' + c.line_number
    : c.file_path;
  meta.textContent = where ? where + ' · ' + formatDate(c.created_at) : formatDate(c.created_at);
  const body = el('div', { class: 'comment-body' });
  body.textContent = c.body;
  const del = el('button', { class: 'btn-icon comment-delete' });
  del.title = 'Delete';
  del.textContent = '×';
  del.addEventListener('click', async () => {
    await api('DELETE', '/api/projects/' + slug + '/reviews/' + reviewId + '/comments/' + c.id);
    div.remove();
  });
  div.appendChild(meta);
  div.appendChild(body);
  div.appendChild(del);
  return div;
}

