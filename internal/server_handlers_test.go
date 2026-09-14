package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const handlerOutsideSHA = "ffffffffffffffffffffffffffffffffffffffff"

func handlerSeedReview(t *testing.T) (*fixture, *Review) {
	t.Helper()
	f := newFixture(t)
	f.Commit("README.md", "base\n", "base commit")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "a1\n", "add a")
	f.Commit("b.txt", "line1\nline2\nline3\nline4\nline5\n", "add b")
	f.Commit("a.txt", "a2\n", "update a")
	f.Push("feat")
	rev := f.postReceive("feat")
	return f, rev
}

func handlerReviewPath(rev *Review, suffix string) string {
	return "/api/projects/proj/reviews/" + strconv.FormatInt(rev.ID, 10) + suffix
}

func handlerNewEmptyServer(t *testing.T) http.Handler {
	t.Helper()
	db := openTestDB(t)
	srv := New(&Config{Port: testPort, DataDir: t.TempDir()}, db)
	return srv.Handler()
}

func handlerBrokenProject(t *testing.T, f *fixture) *Project {
	t.Helper()
	p, err := f.db.CreateProject("broken", filepath.Join(t.TempDir(), "gone", "repo.git"), "main")
	if err != nil {
		t.Fatalf("create broken project: %v", err)
	}
	if _, err := f.db.UpsertReview(p.ID, UpsertReviewParams{
		Branch: "x", BaseBranch: "main", Title: "x", HeadSHA: "deadbeef", BaseSHA: "deadbeef",
	}); err != nil {
		t.Fatalf("upsert broken review: %v", err)
	}
	return p
}

func handlerErrBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	if body["error"] == "" {
		t.Fatalf("expected non-empty error field, got %q", rec.Body.String())
	}
	return body["error"]
}

func TestServeIndexHTML(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	if !strings.Contains(rec.Body.String(), "<title>Reviewer</title>") {
		t.Fatalf("body missing expected title marker: %s", rec.Body.String())
	}
}

func TestHighlightCSSEndpoint(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/highlight.css", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	if !strings.Contains(rec.Body.String(), "html.dark") {
		t.Fatalf("body missing dark-theme scoping: %s", rec.Body.String())
	}
}

func TestListProjectsEmpty(t *testing.T) {
	h := handlerNewEmptyServer(t)
	rec := testDo(t, h, "GET", "/api/projects", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want []", got)
	}
}

func TestListProjectsNonEmpty(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/api/projects", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	projects := decode[[]*Project](t, rec)
	if len(projects) != 1 || projects[0].Slug != "proj" {
		t.Fatalf("projects = %+v, want one project with slug proj", projects)
	}
}

func TestGetProjectUnknownSlug(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/api/projects/nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	handlerErrBody(t, rec)
}

func TestGetProjectFound(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/api/projects/proj", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	p := decode[*Project](t, rec)
	if p.Slug != "proj" || p.BaseBranch != "main" {
		t.Fatalf("project = %+v", p)
	}
}

func TestReviewInvalidID(t *testing.T) {
	f, _ := handlerSeedReview(t)
	rec := f.do("GET", "/api/projects/proj/reviews/notanumber/comments", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	handlerErrBody(t, rec)
}

func TestReviewCrossProjectNotFound(t *testing.T) {
	f, rev := handlerSeedReview(t)
	if _, err := f.db.CreateProject("other", filepath.Join(t.TempDir(), "other.git"), "main"); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	rec := f.do("GET", "/api/projects/other/reviews/"+strconv.FormatInt(rev.ID, 10)+"/comments", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	handlerErrBody(t, rec)
}

func TestDiffRangeValidations(t *testing.T) {
	f, rev := handlerSeedReview(t)
	boundaries, err := CommitBoundaries(f.Bare, rev.BaseSHA, rev.HeadSHA)
	if err != nil {
		t.Fatalf("commit boundaries: %v", err)
	}
	if len(boundaries) != 4 {
		t.Fatalf("boundaries = %v, want 4 entries", boundaries)
	}
	shaA1, shaB := boundaries[1], boundaries[2]

	cases := []struct {
		name  string
		query string
	}{
		{"from without to", "from=" + shaA1},
		{"sha outside boundaries", "from=" + handlerOutsideSHA + "&to=" + shaB},
		{"from equals to", "from=" + shaB + "&to=" + shaB},
		{"from after to", "from=" + shaB + "&to=" + shaA1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := f.do("GET", handlerReviewPath(rev, "/diff/parsed?"+c.query), nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
			handlerErrBody(t, rec)
		})
	}
}

func TestDiffRangeValidSpan(t *testing.T) {
	f, rev := handlerSeedReview(t)
	boundaries, err := CommitBoundaries(f.Bare, rev.BaseSHA, rev.HeadSHA)
	if err != nil {
		t.Fatalf("commit boundaries: %v", err)
	}
	shaA1, shaB := boundaries[1], boundaries[2]

	rec := f.do("GET", handlerReviewPath(rev, "/diff/parsed?from="+shaA1+"&to="+shaB), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	files := decode[[]HighlightedFile](t, rec)
	if len(files) != 1 || files[0].Path != "b.txt" || files[0].Status != "A" {
		t.Fatalf("files = %+v, want just an added b.txt", files)
	}
}

func TestDiffRangeSinceLastReview(t *testing.T) {
	f, rev := handlerSeedReview(t)

	patchRec := f.do("PATCH", handlerReviewPath(rev, ""), map[string]string{"status": "approved"})
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status: %d: %s", patchRec.Code, patchRec.Body)
	}

	f.Commit("c.txt", "c1\n", "add c")
	f.Push("feat")
	rev2 := f.postReceive("feat")

	rec := f.do("GET", handlerReviewPath(rev2, "/diff/parsed?since=last_review"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	files := decode[[]HighlightedFile](t, rec)
	if len(files) != 1 || files[0].Path != "c.txt" || files[0].Status != "A" {
		t.Fatalf("files = %+v, want just an added c.txt", files)
	}
}

func TestDiffRangeSinceLastReviewFallsBackToBase(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "base\n", "base commit")
	f.Push("main")
	f.Branch("feat2")
	f.Commit("x.txt", "x1\n", "add x")
	f.Push("feat2")
	rev := f.postReceive("feat2")

	withoutSince := f.do("GET", handlerReviewPath(rev, "/diff/parsed"), nil)
	withSince := f.do("GET", handlerReviewPath(rev, "/diff/parsed?since=last_review"), nil)
	if withoutSince.Code != http.StatusOK || withSince.Code != http.StatusOK {
		t.Fatalf("status without=%d with=%d", withoutSince.Code, withSince.Code)
	}
	if withoutSince.Body.String() != withSince.Body.String() {
		t.Fatalf("since=last_review with no prior review should match base diff:\nwithout=%s\nwith=%s",
			withoutSince.Body, withSince.Body)
	}
}

func TestDiffRangeBrokenRepo(t *testing.T) {
	f, _ := handlerSeedReview(t)
	p := handlerBrokenProject(t, f)
	revs, err := f.db.ListReviews(p.ID, "")
	if err != nil || len(revs) != 1 {
		t.Fatalf("list broken reviews: %v %+v", err, revs)
	}
	rec := f.do("GET", "/api/projects/broken/reviews/"+strconv.FormatInt(revs[0].ID, 10)+"/diff/parsed", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body)
	}
	handlerErrBody(t, rec)
}

func TestFileContentMissingParams(t *testing.T) {
	f, rev := handlerSeedReview(t)
	boundaries, err := CommitBoundaries(f.Bare, rev.BaseSHA, rev.HeadSHA)
	if err != nil {
		t.Fatalf("commit boundaries: %v", err)
	}
	sha := boundaries[2]

	cases := []struct {
		name  string
		query string
	}{
		{"missing path", "sha=" + sha},
		{"missing sha", "path=b.txt"},
		{"missing both", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := f.do("GET", handlerReviewPath(rev, "/file-content?"+c.query), nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			handlerErrBody(t, rec)
		})
	}
}

func TestFileContentShaOutsideBoundaries(t *testing.T) {
	f, rev := handlerSeedReview(t)
	rec := f.do("GET", handlerReviewPath(rev, "/file-content?path=b.txt&sha="+handlerOutsideSHA), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	handlerErrBody(t, rec)
}

type handlerFileContentResp struct {
	Lines []string `json:"lines"`
	Start int      `json:"start"`
	Total int      `json:"total"`
	HTML  []string `json:"html"`
}

func TestFileContentWindow(t *testing.T) {
	f, rev := handlerSeedReview(t)
	boundaries, err := CommitBoundaries(f.Bare, rev.BaseSHA, rev.HeadSHA)
	if err != nil {
		t.Fatalf("commit boundaries: %v", err)
	}
	sha := boundaries[2]

	rec := f.do("GET", handlerReviewPath(rev, "/file-content?path=b.txt&sha="+sha+"&start=2&count=2"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	resp := decode[handlerFileContentResp](t, rec)
	if resp.Start != 2 || resp.Total != 5 || len(resp.Lines) != 2 ||
		resp.Lines[0] != "line2" || resp.Lines[1] != "line3" {
		t.Fatalf("resp = %+v, want lines 2-3 of a 5-line file", resp)
	}
}

func TestFileContentInvalidWindowParamsIgnored(t *testing.T) {
	f, rev := handlerSeedReview(t)
	boundaries, err := CommitBoundaries(f.Bare, rev.BaseSHA, rev.HeadSHA)
	if err != nil {
		t.Fatalf("commit boundaries: %v", err)
	}
	sha := boundaries[2]

	cases := []string{"start=abc", "start=0", "start=-1", "count=abc", "count=0", "count=-3"}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			rec := f.do("GET", handlerReviewPath(rev, "/file-content?path=b.txt&sha="+sha+"&"+q), nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
			}
			resp := decode[handlerFileContentResp](t, rec)
			if resp.Start != 1 || resp.Total != 5 || len(resp.Lines) != 5 {
				t.Fatalf("resp = %+v, want the full 5-line file from start 1", resp)
			}
		})
	}
}

func TestFileContentHighlight(t *testing.T) {
	f, rev := handlerSeedReview(t)
	boundaries, err := CommitBoundaries(f.Bare, rev.BaseSHA, rev.HeadSHA)
	if err != nil {
		t.Fatalf("commit boundaries: %v", err)
	}
	sha := boundaries[2]

	rec := f.do("GET", handlerReviewPath(rev, "/file-content?path=b.txt&sha="+sha+"&start=2&count=2&highlight=true"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	resp := decode[handlerFileContentResp](t, rec)
	if len(resp.HTML) != len(resp.Lines) {
		t.Fatalf("html length = %d, want %d to line up with the window", len(resp.HTML), len(resp.Lines))
	}
	for i, html := range resp.HTML {
		if !strings.Contains(html, resp.Lines[i]) {
			t.Fatalf("html[%d] = %q, want it to contain %q", i, html, resp.Lines[i])
		}
	}
}

func TestFileContentMissingFile(t *testing.T) {
	f, rev := handlerSeedReview(t)
	boundaries, err := CommitBoundaries(f.Bare, rev.BaseSHA, rev.HeadSHA)
	if err != nil {
		t.Fatalf("commit boundaries: %v", err)
	}
	sha := boundaries[3]

	rec := f.do("GET", handlerReviewPath(rev, "/file-content?path=nope.txt&sha="+sha), nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	handlerErrBody(t, rec)
}

func TestCommitsOrder(t *testing.T) {
	f, rev := handlerSeedReview(t)
	rec := f.do("GET", handlerReviewPath(rev, "/commits"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	commits := decode[[]Commit](t, rec)
	if len(commits) != 3 {
		t.Fatalf("commits = %+v, want 3", commits)
	}
	wantMsgs := []string{"add a", "add b", "update a"}
	for i, c := range commits {
		if c.Message != wantMsgs[i] {
			t.Fatalf("commits[%d].Message = %q, want %q (oldest-first)", i, c.Message, wantMsgs[i])
		}
	}
}

func TestCommitsBrokenRepoGitError(t *testing.T) {
	f, _ := handlerSeedReview(t)
	p := handlerBrokenProject(t, f)
	revs, err := f.db.ListReviews(p.ID, "")
	if err != nil || len(revs) != 1 {
		t.Fatalf("list broken reviews: %v %+v", err, revs)
	}
	rec := f.do("GET", "/api/projects/broken/reviews/"+strconv.FormatInt(revs[0].ID, 10)+"/commits", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body)
	}
	handlerErrBody(t, rec)
}

func TestListCommentsEmptyAndPopulated(t *testing.T) {
	f, rev := handlerSeedReview(t)

	rec := f.do("GET", handlerReviewPath(rev, "/comments"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want []", got)
	}

	createRec := f.do("POST", handlerReviewPath(rev, "/comments"), map[string]string{"body": "hello"})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create comment: %d: %s", createRec.Code, createRec.Body)
	}

	rec = f.do("GET", handlerReviewPath(rev, "/comments"), nil)
	comments := decode[[]*Comment](t, rec)
	if len(comments) != 1 || comments[0].Body != "hello" {
		t.Fatalf("comments = %+v", comments)
	}
}

func TestCreateCommentValidation(t *testing.T) {
	f, rev := handlerSeedReview(t)

	cases := []struct {
		name string
		body any
	}{
		{"empty body", map[string]any{"body": ""}},
		{"line_end without line_number", map[string]any{"body": "x", "line_end": 5}},
		{"line_end less than line_number", map[string]any{"body": "x", "line_number": 5, "line_end": 3}},
		{"invalid JSON", "{not-json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := f.do("POST", handlerReviewPath(rev, "/comments"), c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
			handlerErrBody(t, rec)
		})
	}
}

func TestCreateCommentValid(t *testing.T) {
	f, rev := handlerSeedReview(t)
	rec := f.do("POST", handlerReviewPath(rev, "/comments"), map[string]any{
		"body": "looks good", "file_path": "a.txt", "line_number": 2, "line_end": 4,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	c := decode[*Comment](t, rec)
	if c.Body != "looks good" || c.FilePath == nil || *c.FilePath != "a.txt" ||
		c.LineNumber == nil || *c.LineNumber != 2 || c.LineEnd == nil || *c.LineEnd != 4 {
		t.Fatalf("comment = %+v", c)
	}
}

func TestCreateCommentLineEndEqualsLineNumberStoredNull(t *testing.T) {
	f, rev := handlerSeedReview(t)
	rec := f.do("POST", handlerReviewPath(rev, "/comments"), map[string]any{
		"body": "single line", "file_path": "a.txt", "line_number": 4, "line_end": 4,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	c := decode[*Comment](t, rec)
	if c.LineNumber == nil || *c.LineNumber != 4 || c.LineEnd != nil {
		t.Fatalf("comment = %+v, want line_end nil", c)
	}

	stored, err := f.db.ListComments(rev.ID)
	if err != nil || len(stored) != 1 || stored[0].LineEnd != nil {
		t.Fatalf("stored comments = %+v, err = %v, want one comment with line_end NULL", stored, err)
	}
}

func TestDeleteCommentInvalidID(t *testing.T) {
	f, rev := handlerSeedReview(t)
	rec := f.do("DELETE", handlerReviewPath(rev, "/comments/notanumber"), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	handlerErrBody(t, rec)
}

func TestDBOutageReturns500(t *testing.T) {
	f, rev := handlerSeedReview(t)
	boundaries, err := CommitBoundaries(f.Bare, rev.BaseSHA, rev.HeadSHA)
	if err != nil {
		t.Fatalf("commit boundaries: %v", err)
	}
	sha := boundaries[2]
	reviewPrefix := "/api/projects/proj/reviews/" + strconv.FormatInt(rev.ID, 10)

	if err := f.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"list projects", "GET", "/api/projects", nil},
		{"get project", "GET", "/api/projects/proj", nil},
		{"diff parsed", "GET", reviewPrefix + "/diff/parsed", nil},
		{"file content", "GET", reviewPrefix + "/file-content?path=b.txt&sha=" + sha, nil},
		{"commits", "GET", reviewPrefix + "/commits", nil},
		{"list comments", "GET", reviewPrefix + "/comments", nil},
		{"create comment", "POST", reviewPrefix + "/comments", map[string]string{"body": "x"}},
		{"delete comment", "DELETE", reviewPrefix + "/comments/1", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := f.do(c.method, c.path, c.body)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body)
			}
			handlerErrBody(t, rec)
		})
	}
}
