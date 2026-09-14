package internal

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func syncItoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func TestListReviewsEmpty(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/api/projects/proj/reviews", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want []", got)
	}
}

func TestListReviewsBranchFilter(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")
	rev := f.postReceive("feat")

	rec := f.do("GET", "/api/projects/proj/reviews?branch=nope", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("unknown branch: body = %q, want []", got)
	}

	rec = f.do("GET", "/api/projects/proj/reviews?branch=feat", nil)
	revs := decode[[]*Review](t, rec)
	if len(revs) != 1 || revs[0].ID != rev.ID {
		t.Fatalf("branch filter: got %+v, want single review %d", revs, rev.ID)
	}
}

func TestGetReviewMergedFlag(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")
	rev := f.postReceive("feat")

	f.Checkout("main")
	f.MergeNoFF("feat")
	f.Push("main")

	rec := f.do("GET", "/api/projects/proj/reviews/"+syncItoa(rev.ID), nil)
	got := decode[*Review](t, rec)
	if got.Merged {
		t.Fatalf("open review reported merged; markMerged must skip open reviews")
	}

	if _, err := f.db.UpdateReviewStatus(rev.ID, "approved"); err != nil {
		t.Fatalf("update status: %v", err)
	}
	rec = f.do("GET", "/api/projects/proj/reviews/"+syncItoa(rev.ID), nil)
	got = decode[*Review](t, rec)
	if !got.Merged {
		t.Fatalf("approved+landed review reported unmerged")
	}
}

func TestListReviewsStatusFilter(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")

	f.Branch("open-one")
	f.Commit("o.txt", "o\n", "add o")
	f.Push("open-one")
	openRev := f.postReceive("open-one")

	f.Checkout("main")
	f.Branch("approved-one")
	f.Commit("a.txt", "a\n", "add a")
	f.Push("approved-one")
	approvedRev := f.postReceive("approved-one")
	if _, err := f.db.UpdateReviewStatus(approvedRev.ID, "approved"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	rec := f.do("GET", "/api/projects/proj/reviews?status=approved", nil)
	revs := decode[[]*Review](t, rec)
	if len(revs) != 1 || revs[0].ID != approvedRev.ID {
		t.Fatalf("status=approved: got %+v, want only %d", revs, approvedRev.ID)
	}

	rec = f.do("GET", "/api/projects/proj/reviews?status=open", nil)
	revs = decode[[]*Review](t, rec)
	if len(revs) != 1 || revs[0].ID != openRev.ID {
		t.Fatalf("status=open: got %+v, want only %d", revs, openRev.ID)
	}
}

func TestListReviewsUnknownProject404(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/api/projects/nope/reviews", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetReviewUnknownProject404(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/api/projects/nope/reviews/1", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestReviewsDBOutageReturns500(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")
	rev := f.postReceive("feat")

	if err := f.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	cases := []struct {
		name string
		path string
	}{
		{"list reviews", "/api/projects/proj/reviews"},
		{"list reviews by branch", "/api/projects/proj/reviews?branch=feat"},
		{"get review", "/api/projects/proj/reviews/" + syncItoa(rev.ID)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := f.do("GET", c.path, nil)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestGetReviewInvalidID(t *testing.T) {
	f := newFixture(t)
	rec := f.do("GET", "/api/projects/proj/reviews/abc", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGetReviewCrossProject404(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")
	rev := f.postReceive("feat")

	otherRepo := filepath.Join(t.TempDir(), "other.git")
	if err := CreateBareRepo(otherRepo); err != nil {
		t.Fatalf("create other repo: %v", err)
	}
	if _, err := f.db.CreateProject("other", otherRepo, "main"); err != nil {
		t.Fatalf("create other project: %v", err)
	}

	rec := f.do("GET", "/api/projects/other/reviews/"+syncItoa(rev.ID), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project get: status = %d, want 404", rec.Code)
	}
}

func TestPostReceiveInvalidJSON(t *testing.T) {
	f := newFixture(t)
	rec := f.do("POST", "/api/hooks/post-receive", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPostReceiveUnknownProject(t *testing.T) {
	f := newFixture(t)
	rec := f.do("POST", "/api/hooks/post-receive", map[string]string{
		"project_slug": "nope", "branch": "feat", "new_sha": strings.Repeat("a", 40),
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPostReceiveBranchMissingFromRepo(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	rec := f.do("POST", "/api/hooks/post-receive", map[string]string{
		"project_slug": f.project.Slug, "branch": "ghost", "new_sha": strings.Repeat("a", 40),
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestPostReceiveBaseBranchIgnored(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	rec := f.do("POST", "/api/hooks/post-receive", map[string]string{
		"project_slug": f.project.Slug, "branch": "main", "new_sha": f.SHA("main"),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["action"] != "ignored_base_branch" {
		t.Fatalf("action = %q, want ignored_base_branch", body["action"])
	}
	rev, err := f.db.GetReviewByBranch(f.project.ID, "main")
	if err != nil {
		t.Fatalf("get review by branch: %v", err)
	}
	if rev != nil {
		t.Fatalf("a review was created for the base branch")
	}
}

func TestPostReceiveZeroSHACloses(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")
	rev := f.postReceive("feat")

	rec := f.do("POST", "/api/hooks/post-receive", map[string]string{
		"project_slug": f.project.Slug, "branch": "feat", "new_sha": strings.Repeat("0", 40),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["action"] != "closed_on_delete" {
		t.Fatalf("action = %q, want closed_on_delete", body["action"])
	}
	got, err := f.db.GetReview(rev.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if got.Status != "closed" {
		t.Fatalf("status = %q, want closed", got.Status)
	}
}

func TestPostReceiveFirstPushNoBaseBranch(t *testing.T) {
	f := newFixture(t)
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")
	rev := f.postReceive("feat")
	if rev.BaseSHA != EmptyTreeSHA {
		t.Fatalf("base sha = %q, want EmptyTreeSHA", rev.BaseSHA)
	}
}

func TestPostReceiveRepushSameHeadKeepsApproved(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")
	rev := f.postReceive("feat")
	if _, err := f.db.UpdateReviewStatus(rev.ID, "approved"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	f.postReceive("feat")

	got, err := f.db.GetReview(rev.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if got.Status != "approved" {
		t.Fatalf("status = %q, want approved (unchanged head must not reopen)", got.Status)
	}
}

func TestPostReceiveNewHeadReopensChangesRequested(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")
	rev := f.postReceive("feat")
	if _, err := f.db.UpdateReviewStatus(rev.ID, "changes_requested"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	f.Commit("a.txt", "hello again\n", "revise a")
	f.Push("feat")
	f.postReceive("feat")

	got, err := f.db.GetReview(rev.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if got.Status != "open" {
		t.Fatalf("status = %q, want open (new head must reopen)", got.Status)
	}
}

func TestPostReceiveOrphanBranchNoMergeBase(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")

	f.Git("checkout", "-q", "--orphan", "orphan-branch")
	f.Git("rm", "-rf", "-q", ".")
	f.Write("orphan.txt", "hi\n")
	f.Git("add", "-A")
	f.Git("commit", "-q", "-m", "orphan init")
	f.Push("orphan-branch")

	rec := f.do("POST", "/api/hooks/post-receive", map[string]string{
		"project_slug": f.project.Slug, "branch": "orphan-branch", "new_sha": f.SHA("orphan-branch"),
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	rev, err := f.db.GetReviewByBranch(f.project.ID, "orphan-branch")
	if err != nil {
		t.Fatalf("get review by branch: %v", err)
	}
	if rev != nil {
		t.Fatalf("a review was created for a branch with no merge base")
	}
}

func TestRetargetComments(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "base", "init")
	f.Push("main")
	f.Branch("feat")

	f.Write("a.txt", "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n")
	f.Write("e.txt", "r1\nr2\nr3\nr4\nr5\n")
	f.Write("b.txt", "b1\nb2\nb3\n")
	f.Write("c.txt", "c1\nc2\nc3\nc4\nc5\nc6\nc7\nc8\n")
	f.Write("d.txt", "unchanged\n")
	f.Git("add", "-A")
	f.Git("commit", "-q", "-m", "feat initial")
	f.Push("feat")
	rev := f.postReceive("feat")

	mk := func(path string, line, end int64) *Comment {
		params := CreateCommentParams{Body: "x", FilePath: path, LineNumber: &line}
		if end != 0 {
			params.LineEnd = &end
		}
		c, err := f.db.CreateComment(rev.ID, params)
		if err != nil {
			t.Fatalf("create comment on %s: %v", path, err)
		}
		return c
	}
	cShift := mk("a.txt", 8, 0)
	cRewrite := mk("e.txt", 3, 0)
	cDeletedFile := mk("b.txt", 2, 0)
	cRange := mk("c.txt", 2, 7)
	cUntouched := mk("d.txt", 1, 0)
	cUnmovedLine := mk("c.txt", 1, 0)
	cUnmovedRange := mk("c.txt", 4, 5)
	cGeneral, err := f.db.CreateComment(rev.ID, CreateCommentParams{Body: "general"})
	if err != nil {
		t.Fatalf("create general comment: %v", err)
	}

	f.Write("a.txt", "one\ntwo\ntwo-point-five\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n")
	f.Write("e.txt", "r1\nr2\nR3-CHANGED\nr4\nr5\n")
	f.Write("c.txt", "c1\nC2-CHANGED\nc3\nc4\nc5\nc6\nC7-CHANGED\nc8\n")
	f.Git("add", "-A")
	f.Git("rm", "-q", "b.txt")
	f.Git("commit", "-q", "-m", "feat revise")
	f.Push("feat")
	f.postReceive("feat")

	comments, err := f.db.ListComments(rev.ID)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	byID := make(map[int64]*Comment, len(comments))
	for _, c := range comments {
		byID[c.ID] = c
	}

	assertAnchor := func(name string, id int64, wantPath *string, wantLine, wantEnd *int64) {
		t.Helper()
		c := byID[id]
		if c == nil {
			t.Fatalf("%s: comment %d not found after retarget", name, id)
		}
		if !ptrEq(c.FilePath, wantPath) {
			t.Fatalf("%s: file_path = %v, want %v", name, derefStr(c.FilePath), derefStr(wantPath))
		}
		if !ptrEq(c.LineNumber, wantLine) {
			t.Fatalf("%s: line_number = %v, want %v", name, derefInt(c.LineNumber), derefInt(wantLine))
		}
		if !ptrEq(c.LineEnd, wantEnd) {
			t.Fatalf("%s: line_end = %v, want %v", name, derefInt(c.LineEnd), derefInt(wantEnd))
		}
	}

	assertAnchor("shift below insertion", cShift.ID, dbPtr("a.txt"), dbPtr(int64(9)), nil)
	assertAnchor("rewritten line becomes file-level", cRewrite.ID, dbPtr("e.txt"), nil, nil)
	assertAnchor("deleted file becomes general", cDeletedFile.ID, nil, nil, nil)
	assertAnchor("partly rewritten range shrinks", cRange.ID, dbPtr("c.txt"), dbPtr(int64(3)), dbPtr(int64(6)))
	assertAnchor("untouched comment unchanged", cUntouched.ID, dbPtr("d.txt"), dbPtr(int64(1)), nil)
	assertAnchor("unmoved single line in a touched file", cUnmovedLine.ID, dbPtr("c.txt"), dbPtr(int64(1)), nil)
	assertAnchor("unmoved range in a touched file", cUnmovedRange.ID, dbPtr("c.txt"), dbPtr(int64(4)), dbPtr(int64(5)))
	assertAnchor("general comment stays general", cGeneral.ID, nil, nil, nil)
}

func ptrEq[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func derefStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func derefInt(n *int64) string {
	if n == nil {
		return "<nil>"
	}
	return syncItoa(*n)
}

func TestResyncSyncsBranchWithNoHookCall(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")

	f.srv.Resync()

	rev, err := f.db.GetReviewByBranch(f.project.ID, "feat")
	if err != nil {
		t.Fatalf("get review by branch: %v", err)
	}
	if rev == nil {
		t.Fatalf("resync did not create a review for a pushed branch")
	}
	if rev.HeadSHA != f.SHA("feat") {
		t.Fatalf("head sha = %q, want %q", rev.HeadSHA, f.SHA("feat"))
	}
}

func TestResyncDeletedBranchClosesOpenNotApproved(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")

	f.Branch("open-branch")
	f.Commit("o.txt", "o\n", "add o")
	f.Push("open-branch")
	openRev := f.postReceive("open-branch")

	f.Checkout("main")
	f.Branch("approved-branch")
	f.Commit("ap.txt", "a\n", "add a")
	f.Push("approved-branch")
	approvedRev := f.postReceive("approved-branch")
	if _, err := f.db.UpdateReviewStatus(approvedRev.ID, "approved"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	f.Push(":open-branch", ":approved-branch")

	f.srv.Resync()

	gotOpen, err := f.db.GetReview(openRev.ID)
	if err != nil {
		t.Fatalf("get open review: %v", err)
	}
	if gotOpen.Status != "closed" {
		t.Fatalf("open-branch status = %q, want closed", gotOpen.Status)
	}
	gotApproved, err := f.db.GetReview(approvedRev.ID)
	if err != nil {
		t.Fatalf("get approved review: %v", err)
	}
	if gotApproved.Status != "approved" {
		t.Fatalf("approved-branch status = %q, want approved (must not be closed)", gotApproved.Status)
	}
}

func TestResyncSkipsBaseBranch(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")

	f.srv.Resync()

	rev, err := f.db.GetReviewByBranch(f.project.ID, "main")
	if err != nil {
		t.Fatalf("get review by branch: %v", err)
	}
	if rev != nil {
		t.Fatalf("resync created a review for the base branch")
	}
}

func TestResyncSkipsBrokenProjectButSyncsOthers(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")

	if _, err := f.db.CreateProject("bad", filepath.Join(t.TempDir(), "missing.git"), "main"); err != nil {
		t.Fatalf("create broken project: %v", err)
	}

	f.srv.Resync()

	rev, err := f.db.GetReviewByBranch(f.project.ID, "feat")
	if err != nil {
		t.Fatalf("get review by branch: %v", err)
	}
	if rev == nil {
		t.Fatalf("resync did not sync the valid project's branch when another project's repo path is missing")
	}
}

func TestResyncNoOpWhenHeadUnchanged(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")

	f.srv.Resync()
	first, err := f.db.GetReviewByBranch(f.project.ID, "feat")
	if err != nil || first == nil {
		t.Fatalf("get review by branch: %v, %v", first, err)
	}
	const stamp = "2000-01-01 00:00:00"
	dbSetTimestamp(t, f.db, "reviews", "updated_at", first.ID, stamp)

	f.srv.Resync()
	second, err := f.db.GetReviewByBranch(f.project.ID, "feat")
	if err != nil || second == nil {
		t.Fatalf("get review by branch: %v, %v", second, err)
	}
	if second.UpdatedAt != stamp {
		t.Fatalf("resync re-synced an unchanged head: updated_at %q -> %q", stamp, second.UpdatedAt)
	}
}

func TestResyncLeavesOpenReviewAloneWhenBranchStillExists(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")
	rev := f.postReceive("feat")

	f.srv.Resync()

	got, err := f.db.GetReview(rev.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if got.Status != "open" {
		t.Fatalf("status = %q, want open (branch still exists, resync must leave it alone)", got.Status)
	}
}

func TestResyncSyncErrorLoggedButOtherBranchesContinue(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")

	f.Branch("good-branch")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("good-branch")

	f.Checkout("main")
	f.Git("checkout", "-q", "--orphan", "orphan-branch")
	f.Git("rm", "-rf", "-q", ".")
	f.Write("orphan.txt", "hi\n")
	f.Git("add", "-A")
	f.Git("commit", "-q", "-m", "orphan init")
	f.Push("orphan-branch")

	f.srv.Resync()

	goodRev, err := f.db.GetReviewByBranch(f.project.ID, "good-branch")
	if err != nil {
		t.Fatalf("get review by branch: %v", err)
	}
	if goodRev == nil {
		t.Fatalf("resync did not sync good-branch after orphan-branch failed to sync")
	}
	orphanRev, err := f.db.GetReviewByBranch(f.project.ID, "orphan-branch")
	if err != nil {
		t.Fatalf("get review by branch: %v", err)
	}
	if orphanRev != nil {
		t.Fatalf("a review was created for a branch with no merge base")
	}
}

func TestStartResyncZeroDoesNotSync(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")

	f.srv.StartResync(0)

	rev, err := f.db.GetReviewByBranch(f.project.ID, "feat")
	if err != nil {
		t.Fatalf("get review by branch: %v", err)
	}
	if rev != nil {
		t.Fatalf("StartResync(0) synced a branch; it must be a no-op")
	}
}

func TestStartResyncSyncsOnceImmediately(t *testing.T) {
	f := newFixture(t)
	f.Commit("README.md", "x", "init")
	f.Push("main")
	f.Branch("feat")
	f.Commit("a.txt", "hello\n", "add a")
	f.Push("feat")

	f.srv.StartResync(24 * time.Hour)

	rev, err := f.db.GetReviewByBranch(f.project.ID, "feat")
	if err != nil {
		t.Fatalf("get review by branch: %v", err)
	}
	if rev == nil {
		t.Fatalf("StartResync did not sync immediately before the ticker interval elapsed")
	}
}
