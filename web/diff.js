import { api } from './api.js';
import { el, formatDate, toast } from './dom.js';

export function commentableLine(line, reviewCtx) {
  return reviewCtx.canComment !== false && !!line && line.right != null;
}

// Why a click did not open a comment form, or null when there is nothing to
// explain (empty filler cells in side-by-side).
export function noCommentReason(line, reviewCtx) {
  if (reviewCtx.canComment === false) {
    return 'Comments are off while commits are selected — these line numbers belong to the selection, not the branch head. Clear the selection to comment.';
  }
  if (line && line.right == null) {
    return 'Removed lines can\'t be commented on — comment on a nearby line that is still in the file.';
  }
  return null;
}

export function anchorLine(c) {
  return c.line_end != null ? c.line_end : c.line_number;
}

export function strayComments(comments, filePath, renderedLines, allRendered) {
  return comments.filter(c => {
    if (c.file_path !== filePath) return false;
    if (c.line_number == null) return true;
    if (allRendered) return false;
    return !renderedLines.has(anchorLine(c));
  });
}

export function buildCommentMap(comments) {
  const map = {};
  for (const c of comments) {
    if (!c.file_path || c.line_number == null) continue;
    const key = c.file_path + ':' + anchorLine(c);
    if (!map[key]) map[key] = [];
    map[key].push(c);
  }
  return map;
}

export function renderDiffView(files, comments, reviewCtx, fullFileMode, collapseMap, diffMode, diffState) {
  const commentMap = reviewCtx.canComment === false ? {} : buildCommentMap(comments);

  // ch-chroma scopes the generated syntax-highlight CSS to this subtree.
  const wrapper = el('div', { class: 'diff-wrapper ch-chroma' });

  for (const file of files) {
    const filePath = file.path || file.old_path;
    const isDeleted = file.status === 'D';
    // A rename carries a distinct old_path; the diff block is still keyed on the
    // new path (filePath) so pill scroll targets and comment paths line up.
    const isRename = !isDeleted && file.old_path && file.old_path !== file.path;
    const safeId = 'file-' + filePath.replace(/[^a-zA-Z0-9]/g, '_');
    const fileEl = el('div', { class: 'diff-file', id: safeId });

    const isCollapsed = collapseMap ? !!collapseMap.get(filePath) : false;
    const header = el('div', { class: 'diff-file-header' });

    const collapseBtn = el('button', { class: 'file-collapse-btn', title: isCollapsed ? 'Expand' : 'Collapse' });
    collapseBtn.textContent = isCollapsed ? '▶' : '▼';
    header.appendChild(collapseBtn);

    const headerText = el('span', { class: 'diff-file-name' });
    headerText.textContent = isRename ? file.old_path + ' → ' + file.path : filePath;
    header.appendChild(headerText);

    if (isDeleted) {
      const tag = el('span', { class: 'file-tag file-tag-deleted' });
      tag.textContent = 'deleted';
      header.appendChild(tag);
    } else if (isRename) {
      const tag = el('span', { class: 'file-tag file-tag-renamed' });
      tag.textContent = 'renamed';
      header.appendChild(tag);
    }

    // A deleted file has no content at head, so "Full file" doesn't apply.
    if (!file.binary && !isDeleted) {
      const fullBtn = el('button', { class: 'btn btn-sm file-fullfile-btn' });
      const isFullFile = fullFileMode && fullFileMode.get(filePath);
      fullBtn.textContent = isFullFile ? 'Hunks only' : 'Full file';
      fullBtn.addEventListener('click', (e) => {
        e.stopPropagation(); // prevent header collapse toggle
        const current = fullFileMode.get(filePath);
        fullFileMode.set(filePath, !current);
        const fresh = renderDiffView(files, comments, reviewCtx, fullFileMode, collapseMap, diffMode, diffState);
        diffState.el.replaceWith(fresh);
        diffState.el = fresh;
      });
      header.appendChild(fullBtn);
    }
    fileEl.appendChild(header);

    const body = el('div', { class: 'diff-file-body' + (isCollapsed ? ' hidden' : '') });

    if (reviewCtx.canComment !== false) {
      const renderedLines = new Set();
      for (const hunk of (file.hunks || [])) {
        for (const line of hunk.lines) {
          if (line.right != null) renderedLines.add(line.right);
        }
      }
      const allRendered = !!(fullFileMode && fullFileMode.get(filePath));
      for (const c of strayComments(comments, filePath, renderedLines, allRendered)) {
        const fc = el('div', { class: 'file-level-comment' });
        const note = el('div', { class: 'file-level-note' });
        const span = c.line_end != null
          ? 'lines ' + c.line_number + '–' + c.line_end
          : 'line ' + c.line_number;
        note.textContent = c.line_number == null
          ? 'On this file — the line it was written against is gone.'
          : 'On ' + span + ', which this diff does not show.';
        fc.appendChild(note);
        fc.appendChild(makeCommentBlock(c, reviewCtx));
        body.appendChild(fc);
      }
    }

    // Clicking anywhere on the header (except the Full file button) toggles collapse.
    header.addEventListener('click', () => {
      const nowCollapsed = !body.classList.contains('hidden');
      body.classList.toggle('hidden', nowCollapsed);
      collapseBtn.textContent = nowCollapsed ? '▶' : '▼';
      collapseBtn.title = nowCollapsed ? 'Expand' : 'Collapse';
      if (collapseMap) collapseMap.set(filePath, nowCollapsed);
    });

    if (file.binary) {
      const msg = el('div', { class: 'diff-binary' });
      msg.textContent = 'Binary file — content not shown';
      body.appendChild(msg);
      fileEl.appendChild(body);
      wrapper.appendChild(fileEl);
      continue;
    }

    // Full file mode: fetch and render the entire file
    if (fullFileMode && fullFileMode.get(filePath)) {
      renderFullFile(body, file, commentMap, reviewCtx);
      fileEl.appendChild(body);
      wrapper.appendChild(fileEl);
      continue;
    }

    if (!file.hunks || file.hunks.length === 0) {
      // A pure rename or a mode-only change has no hunks; say so rather than
      // showing an empty body.
      const msg = el('div', { class: 'diff-binary' });
      msg.textContent = isRename ? 'Renamed with no content changes' : 'No content changes';
      body.appendChild(msg);
      fileEl.appendChild(body);
      wrapper.appendChild(fileEl);
      continue;
    }

    const table = el('table', { class: 'diff-table' + (diffMode === 'side-by-side' ? ' diff-sbs' : '') });
    const tbody = document.createElement('tbody');

    let prevHunkEndRight = null;

    for (let hi = 0; hi < file.hunks.length; hi++) {
      const hunk = file.hunks[hi];
      const nextHunk = file.hunks[hi + 1] || null;

      // Expand-above row: shown when there are hidden lines between the previous hunk and this one
      if (prevHunkEndRight !== null && hunk.start_right > prevHunkEndRight) {
        tbody.appendChild(makeExpandRow('above', reviewCtx, filePath, prevHunkEndRight, hunk.start_right - 1));
      }

      const hunkRow = document.createElement('tr');
      hunkRow.className = 'diff-hunk';
      const hunkCell = el('td', { colspan: '4' });
      hunkCell.textContent = hunk.header;
      hunkRow.appendChild(hunkCell);
      tbody.appendChild(hunkRow);

      if (diffMode === 'side-by-side') {
        buildSideBySideHunkRows(hunk, filePath, commentMap, reviewCtx)
          .forEach(r => tbody.appendChild(r));
      } else {
        for (let li = 0; li < hunk.lines.length; li++) {
          const line = hunk.lines[li];
          const hlHTML = line.html || null;
          const row = makeDiffRow(line, filePath, hlHTML);
          if (!commentableLine(line, reviewCtx)) row.classList.add('diff-line-static');
          row.addEventListener('click', (e) => {
            if (e.target.closest('.inline-comment') || e.target.closest('.comment-form')) return;
            toggleCommentForm(tbody, row, line, filePath, reviewCtx, e.shiftKey);
          });
          tbody.appendChild(row);

          for (const c of (commentMap[filePath + ':' + line.right] || [])) {
            tbody.appendChild(makeCommentRow(c, reviewCtx));
          }
        }
      }

      // Expand-below row: sits after this hunk's content, before the next hunk
      if (nextHunk) {
        const belowGap = nextHunk.start_right - hunk.end_right;
        if (belowGap > 0) {
          tbody.appendChild(makeExpandRow('below', reviewCtx, filePath, hunk.end_right, nextHunk.start_right - 1));
        }
      }

      prevHunkEndRight = hunk.end_right;
    }

    table.appendChild(tbody);
    const wrap = el('div', { class: 'diff-table-wrap' });
    wrap.appendChild(table);
    body.appendChild(wrap);
    fileEl.appendChild(body);
    wrapper.appendChild(fileEl);
  }
  return wrapper;
}

export async function renderFullFile(fileEl, file, commentMap, reviewCtx) {
  const { slug, reviewId, headSha, highlightEnabled } = reviewCtx;
  const filePath = file.path || file.old_path;

  // Show loading indicator
  const loading = el('div', { class: 'muted' });
  loading.style.padding = '12px';
  loading.textContent = 'Loading full file…';
  fileEl.appendChild(loading);

  const highlightParam = highlightEnabled ? '&highlight=true' : '';
  let data;
  try {
    data = await api('GET', '/api/projects/' + slug + '/reviews/' + reviewId +
      '/file-content?path=' + encodeURIComponent(filePath) +
      '&sha=' + encodeURIComponent(headSha) + highlightParam);
  } catch (e) {
    loading.textContent = 'Error loading file: ' + e.message;
    return;
  }
  loading.remove();

  // Build diff line map: rightLineNum -> line object
  const diffLineMap = new Map();
  for (const hunk of file.hunks) {
    for (const line of hunk.lines) {
      if (line.type === 'add' || line.type === 'ctx') {
        diffLineMap.set(line.right, line);
      }
    }
  }

  // Build highlight map from the API response so all lines — not just those
  // that appear in diff hunks — get highlighted HTML in full-file view.
  const hlLineMap = new Map();
  if (highlightEnabled && data.html) {
    for (let i = 0; i < data.html.length; i++) {
      if (data.html[i]) hlLineMap.set(data.start + i, data.html[i]);
    }
  }

  const table = el('table', { class: 'diff-table' });
  const tbody = document.createElement('tbody');

  for (let i = 0; i < data.lines.length; i++) {
    const lineNum = data.start + i;
    const content = data.lines[i];
    const diffLine = diffLineMap.get(lineNum);

    const lineObj = diffLine || { type: 'ctx', content: content, left: null, right: lineNum };
    const hlHTML = hlLineMap.get(lineNum) || null;
    const row = makeDiffRow(lineObj, filePath, hlHTML);
    if (!commentableLine(lineObj, reviewCtx)) row.classList.add('diff-line-static');
    row.addEventListener('click', (e) => {
      if (e.target.closest('.inline-comment') || e.target.closest('.comment-form')) return;
      toggleCommentForm(tbody, row, lineObj, filePath, reviewCtx, e.shiftKey);
    });
    tbody.appendChild(row);

    // Append comment rows
    const key = filePath + ':' + lineNum;
    for (const c of (commentMap[key] || [])) {
      tbody.appendChild(makeCommentRow(c, reviewCtx));
    }
  }

  table.appendChild(tbody);
  const wrap = el('div', { class: 'diff-table-wrap' });
  wrap.appendChild(table);
  fileEl.appendChild(wrap);
}

export function makeExpandRow(direction, reviewCtx, filePath, fromLine, toLine) {
  const row = el('tr', { class: 'diff-expand-row' });
  const cell = el('td', { colspan: '4', class: 'expand-cell' });
  const btn = el('button', { class: 'btn-expand' });
  btn.textContent = (direction === 'above' ? '▲ ' : '▼ ') + Math.min(20, toLine - fromLine + 1) + ' lines';
  row.dataset.fromLine = fromLine;
  row.dataset.toLine = toLine;

  btn.addEventListener('click', async () => {
    await fetchAndInsertExpandedLines(
      row.parentNode, row, direction,
      reviewCtx, filePath,
      parseInt(row.dataset.fromLine), parseInt(row.dataset.toLine));
  });

  cell.appendChild(btn);
  row.appendChild(cell);
  return row;
}

export async function fetchAndInsertExpandedLines(tbody, refRow, direction, reviewCtx, filePath, fromLine, toLine) {
  const { slug, reviewId, headSha } = reviewCtx;
  const totalGap = toLine - fromLine + 1;
  const count = Math.min(20, totalGap);

  const btn = refRow.querySelector('button');
  if (btn) btn.disabled = true;

  // 'above': fetch the bottom N lines of the gap (closest to the hunk below it)
  // 'below': fetch the top N lines of the gap (closest to the hunk above it)
  const start = direction === 'above' ? toLine - count + 1 : fromLine;

  let data;
  try {
    data = await api('GET', '/api/projects/' + slug + '/reviews/' + reviewId +
      '/file-content?path=' + encodeURIComponent(filePath) +
      '&sha=' + encodeURIComponent(headSha) +
      '&start=' + start +
      '&count=' + count);
  } catch (e) {
    if (btn) { btn.disabled = false; btn.textContent = 'Error'; }
    return;
  }

  // Build rows in ascending line-number order
  const rows = [];
  for (let i = 0; i < data.lines.length; i++) {
    const lineNum = data.start + i;
    const content = data.lines[i];
    const row = el('tr', { class: 'diff-line diff-ctx' });

    const tdLeft = el('td', { class: 'ln' });
    tdLeft.textContent = '';
    const tdRight = el('td', { class: 'ln' });
    tdRight.textContent = lineNum;
    const tdMark = el('td', { class: 'marker' });
    tdMark.textContent = ' ';
    const tdCode = el('td', { class: 'code' });
    tdCode.textContent = content;

    row.appendChild(tdLeft);
    row.appendChild(tdRight);
    row.appendChild(tdMark);
    row.appendChild(tdCode);
    rows.push(row);
  }

  if (direction === 'above') {
    // Insert all rows before the next element after the expand row (the hunk header).
    // Forward iteration: insertBefore(anchor) appends each row before anchor,
    // so rows accumulate in order: row[0], row[1], ..., row[n-1], anchor.
    const anchor = refRow.nextElementSibling;
    for (let i = 0; i < rows.length; i++) {
      refRow.parentNode.insertBefore(rows[i], anchor);
    }
  } else {
    // Insert rows after refRow in ascending order, then move the expand row to the
    // end of the batch so subsequent expansions continue from there (not from the top).
    let insertAfter = refRow;
    for (const row of rows) {
      insertAfter.insertAdjacentElement('afterend', row);
      insertAfter = row;
    }
    insertAfter.insertAdjacentElement('afterend', refRow);
  }

  // Shrink the tracked gap and update or remove the expand row
  const newTo = direction === 'above' ? toLine - count : toLine;
  const newFrom = direction === 'above' ? fromLine : fromLine + count;
  const remaining = newTo - newFrom + 1;

  if (remaining <= 0) {
    refRow.remove();
  } else {
    refRow.dataset.fromLine = newFrom;
    refRow.dataset.toLine = newTo;
    const newBtn = refRow.querySelector('button');
    if (newBtn) {
      newBtn.disabled = false;
      newBtn.textContent = (direction === 'above' ? '▲ ' : '▼ ') + Math.min(20, remaining) + ' lines';
    }
  }
}

export function buildSideBySideHunkRows(hunk, filePath, commentMap, reviewCtx) {
  const rows = [];
  const all = hunk.lines;
  let i = 0;

  while (i < all.length) {
    const line = all[i];

    if (line.type === 'del') {
      const dels = [];
      while (i < all.length && all[i].type === 'del') {
        dels.push(all[i]);
        i++;
      }
      const adds = [];
      while (i < all.length && all[i].type === 'add') {
        adds.push(all[i]);
        i++;
      }
      const maxN = Math.max(dels.length, adds.length);
      for (let j = 0; j < maxN; j++) {
        const leftLine = dels[j] || null;
        const rightLine = adds[j] || null;
        const leftHlHTML = leftLine?.html || null;
        const rightHlHTML = rightLine?.html || null;
        const row = makeSideBySideRow(leftLine, rightLine, filePath, leftHlHTML, rightHlHTML);

        if (!commentableLine(rightLine, reviewCtx)) row.classList.add('diff-line-static');
        row.addEventListener('click', (e) => {
          if (e.target.closest('.inline-comment') || e.target.closest('.comment-form')) return;
          const targetRight = e.target.closest('.code-right') || e.target.closest('.ln-right');
          if (targetRight && rightLine) {
            toggleCommentForm(row.parentNode, row, rightLine, filePath, reviewCtx, e.shiftKey);
          } else {
            toggleCommentForm(row.parentNode, row, leftLine, filePath, reviewCtx, e.shiftKey);
          }
        });
        rows.push(row);

        if (rightLine) {
          for (const c of (commentMap[filePath + ':' + rightLine.right] || [])) {
            rows.push(makeCommentRow(c, reviewCtx));
          }
        }
      }
    } else if (line.type === 'add') {
      const adds = [];
      while (i < all.length && all[i].type === 'add') {
        adds.push(all[i]);
        i++;
      }
      for (let j = 0; j < adds.length; j++) {
        const addLine = adds[j];
        const rightHlHTML = addLine.html || null;
        const row = makeSideBySideRow(null, addLine, filePath, null, rightHlHTML);
        if (!commentableLine(addLine, reviewCtx)) row.classList.add('diff-line-static');
        row.addEventListener('click', (e) => {
          if (e.target.closest('.inline-comment') || e.target.closest('.comment-form')) return;
          const targetRight = e.target.closest('.code-right') || e.target.closest('.ln-right');
          if (targetRight) {
            toggleCommentForm(row.parentNode, row, addLine, filePath, reviewCtx, e.shiftKey);
          } else {
            toggleCommentForm(row.parentNode, row, null, filePath, reviewCtx, e.shiftKey);
          }
        });
        rows.push(row);

        for (const c of (commentMap[filePath + ':' + addLine.right] || [])) {
          rows.push(makeCommentRow(c, reviewCtx));
        }
      }
    } else {
      const hlHTML = line.html || null;
      const row = makeSideBySideRow(line, line, filePath, hlHTML, hlHTML);
      if (!commentableLine(line, reviewCtx)) row.classList.add('diff-line-static');
      row.addEventListener('click', (e) => {
        if (e.target.closest('.inline-comment') || e.target.closest('.comment-form')) return;
        toggleCommentForm(row.parentNode, row, line, filePath, reviewCtx, e.shiftKey);
      });
      rows.push(row);

      const key = filePath + ':' + line.right;
      for (const c of (commentMap[key] || [])) {
        rows.push(makeCommentRow(c, reviewCtx));
      }
      i++;
    }
  }
  return rows;
}

export function makeSideBySideRow(leftLine, rightLine, filePath, leftHlHTML, rightHlHTML) {
  const row = el('tr', { class: 'diff-line' });
  row.dataset.file = filePath;
  if (rightLine && rightLine.right != null) row.dataset.right = rightLine.right;

  const tdLLn = el('td', { class: 'ln ln-left' });
  tdLLn.textContent = leftLine && leftLine.left != null ? leftLine.left : '';

  const tdLCode = el('td', { class: 'code code-left' });
  if (leftLine) {
    if (leftHlHTML) {
      tdLCode.innerHTML = leftHlHTML;
    } else {
      tdLCode.textContent = leftLine.content;
    }
    if (leftLine.type === 'del') tdLCode.classList.add('diff-del');
  }

  const tdRLn = el('td', { class: 'ln ln-right' });
  tdRLn.textContent = rightLine && rightLine.right != null ? rightLine.right : '';

  const tdRCode = el('td', { class: 'code code-right' });
  if (rightLine) {
    if (rightHlHTML) {
      tdRCode.innerHTML = rightHlHTML;
    } else {
      tdRCode.textContent = rightLine.content;
    }
    if (rightLine.type === 'add') tdRCode.classList.add('diff-add');
  }

  row.appendChild(tdLLn);
  row.appendChild(tdLCode);
  row.appendChild(tdRLn);
  row.appendChild(tdRCode);
  return row;
}

export function makeDiffRow(line, filePath, hlHTML) {
  const typeClass = line.type === 'add' ? 'diff-add' : line.type === 'del' ? 'diff-del' : 'diff-ctx';
  const row = el('tr', { class: 'diff-line ' + typeClass });
  row.dataset.file = filePath;
  row.dataset.type = line.type;
  if (line.right != null) row.dataset.right = line.right;

  const tdLeft = el('td', { class: 'ln' });
  tdLeft.textContent = line.left != null ? line.left : '';
  const tdRight = el('td', { class: 'ln' });
  tdRight.textContent = line.right != null ? line.right : '';
  const tdMark = el('td', { class: 'marker' });
  tdMark.textContent = line.type === 'add' ? '+' : line.type === 'del' ? '-' : ' ';
  const tdCode = el('td', { class: 'code' });
  if (hlHTML) {
    tdCode.innerHTML = hlHTML;
  } else {
    tdCode.textContent = line.content;
  }

  row.appendChild(tdLeft);
  row.appendChild(tdRight);
  row.appendChild(tdMark);
  row.appendChild(tdCode);
  return row;
}

export function makeCommentRow(c, reviewCtx) {
  const row = el('tr', { class: 'comment-row' });
  row.dataset.commentId = c.id;
  const cell = el('td', { colspan: '4' });
  cell.appendChild(makeCommentBlock(c, reviewCtx));
  row.appendChild(cell);
  return row;
}

export function makeCommentBlock(c, reviewCtx) {
  const { slug, reviewId } = reviewCtx;
  const div = el('div', { class: 'inline-comment' });

  const meta = el('div', { class: 'comment-meta' });
  meta.textContent = c.line_end != null
    ? 'lines ' + c.line_number + '–' + c.line_end + ' · ' + formatDate(c.created_at)
    : formatDate(c.created_at);
  const body = el('div', { class: 'comment-body' });
  body.textContent = c.body;
  const del = el('button', { class: 'btn-icon comment-delete' });
  del.title = 'Delete comment';
  del.textContent = '×';
  del.addEventListener('click', async () => {
    await api('DELETE', '/api/projects/' + slug + '/reviews/' + reviewId + '/comments/' + c.id);
    (div.closest('tr') || div.closest('.file-level-comment') || div).remove();
  });

  div.appendChild(meta);
  div.appendChild(body);
  div.appendChild(del);
  return div;
}

// Rows in [start, end] of the same file, used to show what a pending range
// comment covers.
export function markRange(tbody, filePath, start, end) {
  for (const row of tbody.querySelectorAll('tr.diff-line')) {
    const n = row.dataset.right;
    if (row.dataset.file !== filePath || n == null) continue;
    row.classList.toggle('comment-range', +n >= start && +n <= end);
  }
}

export function clearRange(tbody) {
  for (const row of tbody.querySelectorAll('tr.comment-range')) {
    row.classList.remove('comment-range');
  }
}

export function rowForLine(tbody, filePath, n) {
  for (const row of tbody.querySelectorAll('tr.diff-line')) {
    if (row.dataset.file === filePath && +row.dataset.right === n) return row;
  }
  return null;
}

export function toggleCommentForm(tbody, lineRow, line, filePath, reviewCtx, shiftKey) {
  const { slug, reviewId } = reviewCtx;
  if (!commentableLine(line, reviewCtx)) {
    const reason = noCommentReason(line, reviewCtx);
    if (reason) toast(reason);
    return;
  }
  const existing = tbody.querySelector('tr.comment-form-row');
  const sameRow = existing && existing.previousElementSibling === lineRow && !shiftKey;
  if (existing) existing.remove();
  clearRange(tbody);
  if (sameRow) {
    reviewCtx.commentAnchor = null;
    return;
  }

  const anchor = reviewCtx.commentAnchor;
  let start = line.right;
  let end = line.right;
  if (shiftKey && anchor && anchor.path === filePath) {
    start = Math.min(anchor.line, line.right);
    end = Math.max(anchor.line, line.right);
  } else {
    reviewCtx.commentAnchor = { path: filePath, line: line.right };
  }
  if (shiftKey) window.getSelection()?.removeAllRanges();
  if (end > start) markRange(tbody, filePath, start, end);
  lineRow = rowForLine(tbody, filePath, end) || lineRow;

  const formRow = el('tr', { class: 'comment-form-row' });
  const cell = el('td', { colspan: '4' });
  const form = el('div', { class: 'comment-form' });

  const textarea = el('textarea');
  textarea.placeholder = end > start
    ? 'Comment on lines ' + start + '–' + end + '…'
    : 'Leave a comment… (shift-click another line to extend)';
  textarea.rows = 3;
  const actions = el('div', { class: 'comment-form-actions' });

  const cancelBtn = el('button', { class: 'btn' });
  cancelBtn.textContent = 'Cancel';
  cancelBtn.addEventListener('click', () => {
    formRow.remove();
    clearRange(tbody);
    reviewCtx.commentAnchor = null;
  });

  const submitBtn = el('button', { class: 'btn btn-primary' });
  submitBtn.textContent = 'Comment';
  submitBtn.addEventListener('click', async () => {
    const body = textarea.value.trim();
    if (!body) return;
    submitBtn.disabled = true;
    try {
      const c = await api('POST', '/api/projects/' + slug + '/reviews/' + reviewId + '/comments', {
        body,
        file_path: filePath,
        line_number: start,
        line_end: end > start ? end : null,
      });
      clearRange(tbody);
      reviewCtx.commentAnchor = null;
      formRow.replaceWith(makeCommentRow(c, reviewCtx));
    } finally {
      submitBtn.disabled = false;
    }
  });

  actions.appendChild(cancelBtn);
  actions.appendChild(submitBtn);
  form.appendChild(textarea);
  form.appendChild(actions);
  cell.appendChild(form);
  formRow.appendChild(cell);
  lineRow.insertAdjacentElement('afterend', formRow);
  textarea.focus();
}
