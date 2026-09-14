This project is a review tool for agent-human collaboration. It provides a local, simple git-based workflow for submitting changes, leaving comments, and approving or requesting revisions.

# Using Git

@~/notes/agent-project-git-hygiene.md
@~/notes/agent-project-review-single.md

# Structure

Keep this section up to date whenever you add, remove, or repurpose a file. This is your quick index into determining where to look for a particular concern.

## Backend (Go)

  cmd/reviewer/main.go
    CLI entry point: serve, init, list, status, wait, response-block, install-hooks commands

  internal/config.go
    Data-dir resolution, derived paths (DBPath, ReposDir, ReviewerURL)

  internal/db.go
    SQLite schema, all DB types (Project, Review, Comment), and every query

  internal/gitops.go
    Git plumbing: bare-repo creation, hook installation, file/commit/diff
    fetching via the git CLI, plus current-repo discovery (remotes, branch)

  internal/diff.go
    Unified-diff parsing into structured files/hunks/lines (HighlightedFile
    model) and the GetParsedDiff orchestration

  internal/highlight.go
    Syntax highlighting via chroma and the generated /highlight.css stylesheet

  internal/server.go
    HTTP server setup, route registration, middleware (logging, CORS)

  internal/server_handlers.go
    All HTTP handlers: projects, reviews, diffs, comments, pre-receive hook

## Frontend (JS/CSS — ES modules)

All files are embedded via web/web.go and served as static assets. The five
scripts are ES modules with explicit imports; index.html loads only the entry
point, and the module graph is:

  api  (no imports)
  dom  (no imports)
  router → dom
  diff   → api, dom
  views  → api, router, dom, diff

  web/index.html
    Single HTML shell; loads the entry point as <script type="module">

  web/style.css
    All styles

  web/api.js
    api() fetch wrapper — the only file that calls fetch

  web/dom.js
    DOM helpers: el(), setApp(), formatDate(), statusBadge(), mergedBadge(),
    errMsg(), btn(), theme helpers and toast()

  web/router.js
    Hash-based router: routes, addRoute(), navigate(), event listeners

  web/diff.js
    Diff rendering: renderDiffView(), renderFullFile(), expand-context,
    side-by-side, comment rows, inline comment form

  web/views.js
    Page views: renderHome(), renderProject(), renderReview() and their
    helpers; registers all routes

# Testing

When testing, use chrome dev tools to verify UI changes and run the server on non-default port to avoid conflicts.

For all testing, the directory "./test-repository contains a sample repository that is registered with the reviewer tool. This repository is for you to test with, and is safe to commit anything to. Don't worry about rolling back or cleaning it up.  Feel free to leave test repo reviews open, especially if  they demonstrate a specific behavior or functionality.

Note that since you're modifying the reviewer itself, to ensure your changes are available kill and rebuild/restart the server before testing. You must also rebuild/restart it after changing web content in order to see the changes.

The reviewer runs on the default port. Tests should run it on a different port, so that you can validate behaviurs against the correct running version.

Tests can be run via `go test ./...` from the project root.  The web UI can be tested by running the server and visiting http://localhost:8091 in a browser.  UI unit tests are to be run via `npm test` from the project root.

# Communication Style

This applies to all human communications including chat, documentation, and code comments:

* Do not use superlatives
* Do not use persuasive writing
* Describe things only in terms of what we do now. Don't include references to what we used to do, or what we do not do.
