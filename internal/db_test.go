package internal

import "testing"

func TestProjectCreateAndGet(t *testing.T) {
	db := openTestDB(t)
	p, err := db.CreateProject("proj-one", "/repo/one", "main")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.Slug != "proj-one" || p.RepoPath != "/repo/one" || p.BaseBranch != "main" {
		t.Errorf("CreateProject result = %+v", p)
	}

	bySlug, err := db.GetProject("proj-one")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if bySlug == nil || bySlug.ID != p.ID {
		t.Errorf("GetProject = %+v, want id %d", bySlug, p.ID)
	}
}

func TestProjectDuplicateSlugRejected(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.CreateProject("dup", "/repo/one", "main"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := db.CreateProject("dup", "/repo/two", "main"); err == nil {
		t.Fatal("CreateProject with duplicate slug: want error, got nil")
	}
}

func TestListProjectsOrderedBySlug(t *testing.T) {
	db := openTestDB(t)
	dbMustCreateProject(t, db, "charlie")
	dbMustCreateProject(t, db, "alpha")
	dbMustCreateProject(t, db, "bravo")

	got, err := db.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, w := range want {
		if got[i].Slug != w {
			t.Errorf("ListProjects()[%d] = %q, want %q", i, got[i].Slug, w)
		}
	}
}

func TestGetProjectUnknownReturnsNil(t *testing.T) {
	db := openTestDB(t)
	if p, err := db.GetProject("nope"); err != nil || p != nil {
		t.Errorf("GetProject(unknown) = %+v, %v, want nil, nil", p, err)
	}
}

func TestUpsertReviewInsertThenUpdate(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "proj")

	first, err := db.UpsertReview(p.ID, UpsertReviewParams{
		Branch: "feature", BaseBranch: "main", Title: "Initial title",
		HeadSHA: "sha1", BaseSHA: "base1",
	})
	if err != nil {
		t.Fatalf("UpsertReview (insert): %v", err)
	}
	if first.Status != "open" {
		t.Errorf("Status = %q, want open", first.Status)
	}
	if first.HeadSHA != "sha1" || first.BaseSHA != "base1" {
		t.Errorf("HeadSHA/BaseSHA = %q/%q, want sha1/base1", first.HeadSHA, first.BaseSHA)
	}

	if _, err := db.UpdateReviewStatus(first.ID, "approved"); err != nil {
		t.Fatalf("UpdateReviewStatus: %v", err)
	}
	dbSetTimestamp(t, db, "reviews", "last_opened_at", first.ID, "2020-01-01 00:00:00")
	dbSetTimestamp(t, db, "reviews", "updated_at", first.ID, "2020-01-01 00:00:00")

	second, err := db.UpsertReview(p.ID, UpsertReviewParams{
		Branch: "feature", BaseBranch: "develop", Title: "Changed title",
		HeadSHA: "sha2", BaseSHA: "base2",
	})
	if err != nil {
		t.Fatalf("UpsertReview (update): %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second push created a new row: ID = %d, want %d", second.ID, first.ID)
	}
	if second.Status != "open" {
		t.Errorf("Status = %q, want open (reopened)", second.Status)
	}
	if second.HeadSHA != "sha2" || second.BaseSHA != "base2" {
		t.Errorf("HeadSHA/BaseSHA = %q/%q, want sha2/base2", second.HeadSHA, second.BaseSHA)
	}
	if second.Title != "Initial title" {
		t.Errorf("Title = %q, want unchanged Initial title", second.Title)
	}
	if second.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want unchanged main", second.BaseBranch)
	}
	if second.LastOpenedAt == "2020-01-01 00:00:00" {
		t.Error("LastOpenedAt was not bumped on reopen")
	}
}

func TestGetReviewUnknownReturnsNil(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "proj")

	if r, err := db.GetReview(99999); err != nil || r != nil {
		t.Errorf("GetReview(unknown) = %+v, %v, want nil, nil", r, err)
	}
	if r, err := db.GetReviewByBranch(p.ID, "nope"); err != nil || r != nil {
		t.Errorf("GetReviewByBranch(unknown) = %+v, %v, want nil, nil", r, err)
	}
}

func TestUpdateReviewStatusSnapshotsHeadSHA(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "proj")
	r := dbMustUpsertReview(t, db, p.ID, "feature")

	got, err := db.UpdateReviewStatus(r.ID, "approved")
	if err != nil {
		t.Fatalf("UpdateReviewStatus: %v", err)
	}
	if got.Status != "approved" {
		t.Errorf("Status = %q, want approved", got.Status)
	}
	if got.LastReviewedSHA != r.HeadSHA {
		t.Errorf("LastReviewedSHA = %q, want %q", got.LastReviewedSHA, r.HeadSHA)
	}
}

func TestUpdateProjectSlugRenamesTheProject(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "old")

	if err := db.UpdateProjectSlug(p.ID, "renamed"); err != nil {
		t.Fatalf("UpdateProjectSlug: %v", err)
	}

	got, err := db.GetProject("renamed")
	if err != nil {
		t.Fatalf("GetProject(renamed): %v", err)
	}
	if got == nil || got.ID != p.ID || got.RepoPath != p.RepoPath {
		t.Errorf("GetProject(renamed) = %+v, want the project that was renamed", got)
	}
	if stale, err := db.GetProject("old"); err != nil || stale != nil {
		t.Errorf("GetProject(old) = %+v, %v, want nil, nil", stale, err)
	}
	if err := db.UpdateProjectSlug(9999, "ghost"); err != nil {
		t.Errorf("UpdateProjectSlug(unknown id) = %v, want nil", err)
	}
}

func TestCloseReviewByBranchOnlyClosesOpen(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "proj")

	open := dbMustUpsertReview(t, db, p.ID, "b-open")
	if err := db.CloseReviewByBranch(p.ID, open.Branch); err != nil {
		t.Fatalf("CloseReviewByBranch: %v", err)
	}
	got, _ := db.GetReviewByBranch(p.ID, open.Branch)
	if got.Status != "closed" {
		t.Errorf("Status = %q, want closed", got.Status)
	}

	approved := dbMustUpsertReview(t, db, p.ID, "b-approved")
	if _, err := db.UpdateReviewStatus(approved.ID, "approved"); err != nil {
		t.Fatalf("UpdateReviewStatus: %v", err)
	}
	if err := db.CloseReviewByBranch(p.ID, approved.Branch); err != nil {
		t.Fatalf("CloseReviewByBranch: %v", err)
	}
	got2, _ := db.GetReviewByBranch(p.ID, approved.Branch)
	if got2.Status != "approved" {
		t.Errorf("Status = %q, want approved (unchanged)", got2.Status)
	}
}

func TestListReviewsAndCountReviews(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "proj")

	r1 := dbMustUpsertReview(t, db, p.ID, "b1")
	dbSetTimestamp(t, db, "reviews", "updated_at", r1.ID, "2024-01-01 00:00:00")

	r2 := dbMustUpsertReview(t, db, p.ID, "b2")
	if _, err := db.UpdateReviewStatus(r2.ID, "approved"); err != nil {
		t.Fatalf("UpdateReviewStatus: %v", err)
	}
	dbSetTimestamp(t, db, "reviews", "updated_at", r2.ID, "2024-01-02 00:00:00")

	r3 := dbMustUpsertReview(t, db, p.ID, "b3")
	dbSetTimestamp(t, db, "reviews", "updated_at", r3.ID, "2024-01-03 00:00:00")

	all, err := db.ListReviews(p.ID, "")
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	if len(all) != 3 || all[0].ID != r3.ID || all[1].ID != r2.ID || all[2].ID != r1.ID {
		t.Errorf("ListReviews order = %v, want [r3 r2 r1]", dbReviewIDs(all))
	}

	open, err := db.ListReviews(p.ID, "open")
	if err != nil {
		t.Fatalf("ListReviews(open): %v", err)
	}
	if len(open) != 2 || open[0].ID != r3.ID || open[1].ID != r1.ID {
		t.Errorf("ListReviews(open) order = %v, want [r3 r1]", dbReviewIDs(open))
	}

	if total, err := db.CountReviews(p.ID, ""); err != nil || total != 3 {
		t.Errorf("CountReviews() = %d, %v, want 3, nil", total, err)
	}
	if n, err := db.CountReviews(p.ID, "open"); err != nil || n != 2 {
		t.Errorf("CountReviews(open) = %d, %v, want 2, nil", n, err)
	}
	if n, err := db.CountReviews(p.ID, "approved"); err != nil || n != 1 {
		t.Errorf("CountReviews(approved) = %d, %v, want 1, nil", n, err)
	}
}

func TestCreateCommentNullableFieldsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "proj")
	r := dbMustUpsertReview(t, db, p.ID, "b1")

	general, err := db.CreateComment(r.ID, CreateCommentParams{Body: "general comment"})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if general.FilePath != nil || general.LineNumber != nil || general.LineEnd != nil {
		t.Errorf("general comment should have nil file/line fields, got %+v", general)
	}

	anchored, err := db.CreateComment(r.ID, CreateCommentParams{
		Body: "anchored comment", FilePath: "a.go",
		LineNumber: dbPtr(int64(5)), LineEnd: dbPtr(int64(10)),
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if anchored.FilePath == nil || *anchored.FilePath != "a.go" {
		t.Errorf("FilePath = %v, want a.go", anchored.FilePath)
	}
	if anchored.LineNumber == nil || *anchored.LineNumber != 5 {
		t.Errorf("LineNumber = %v, want 5", anchored.LineNumber)
	}
	if anchored.LineEnd == nil || *anchored.LineEnd != 10 {
		t.Errorf("LineEnd = %v, want 10", anchored.LineEnd)
	}

	all, err := db.ListComments(r.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("len(ListComments) = %d, want 2", len(all))
	}
}

func TestUpdateCommentAnchor(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "proj")
	r := dbMustUpsertReview(t, db, p.ID, "b1")
	c := dbMustCreateComment(t, db, r.ID, "x")

	if err := db.UpdateCommentAnchor(c.ID, dbPtr("b.go"), dbPtr(int64(7)), nil); err != nil {
		t.Fatalf("UpdateCommentAnchor (set): %v", err)
	}
	got := dbMustGetComment(t, db, r.ID, c.ID)
	if got.FilePath == nil || *got.FilePath != "b.go" {
		t.Errorf("FilePath = %v, want b.go", got.FilePath)
	}
	if got.LineNumber == nil || *got.LineNumber != 7 {
		t.Errorf("LineNumber = %v, want 7", got.LineNumber)
	}
	if got.LineEnd != nil {
		t.Errorf("LineEnd = %v, want nil", got.LineEnd)
	}

	if err := db.UpdateCommentAnchor(c.ID, nil, nil, nil); err != nil {
		t.Fatalf("UpdateCommentAnchor (clear): %v", err)
	}
	got2 := dbMustGetComment(t, db, r.ID, c.ID)
	if got2.FilePath != nil || got2.LineNumber != nil || got2.LineEnd != nil {
		t.Errorf("after clearing anchor, want all nil, got %+v", got2)
	}
}

func TestListCommentsSinceBoundary(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "proj")
	r := dbMustUpsertReview(t, db, p.ID, "b1")

	c1 := dbMustCreateComment(t, db, r.ID, "first")
	c2 := dbMustCreateComment(t, db, r.ID, "second")
	c3 := dbMustCreateComment(t, db, r.ID, "third")
	dbSetTimestamp(t, db, "comments", "created_at", c1.ID, "2024-01-01 00:00:00")
	dbSetTimestamp(t, db, "comments", "created_at", c2.ID, "2024-01-02 00:00:00")
	dbSetTimestamp(t, db, "comments", "created_at", c3.ID, "2024-01-03 00:00:00")

	got, err := db.ListCommentsSince(r.ID, "2024-01-02 00:00:00")
	if err != nil {
		t.Fatalf("ListCommentsSince: %v", err)
	}
	if len(got) != 2 || got[0].ID != c2.ID || got[1].ID != c3.ID {
		t.Errorf("ListCommentsSince = %v, want [c2 c3]", dbCommentIDs(got))
	}
}

func TestCreateCommentOnNonexistentReviewFails(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.CreateComment(999999, CreateCommentParams{Body: "x"}); err == nil {
		t.Fatal("CreateComment on nonexistent review: want error, got nil")
	}
}

func TestDeleteCommentScopedToReview(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "proj")
	r1 := dbMustUpsertReview(t, db, p.ID, "b1")
	r2 := dbMustUpsertReview(t, db, p.ID, "b2")
	c := dbMustCreateComment(t, db, r1.ID, "x")

	if ok, err := db.DeleteComment(r2.ID, c.ID); err != nil || ok {
		t.Errorf("DeleteComment(wrong review) = %v, %v, want false, nil", ok, err)
	}
	if ok, err := db.DeleteComment(r1.ID, c.ID); err != nil || !ok {
		t.Errorf("DeleteComment(correct review) = %v, %v, want true, nil", ok, err)
	}
	if ok, err := db.DeleteComment(r1.ID, c.ID); err != nil || ok {
		t.Errorf("DeleteComment(already gone) = %v, %v, want false, nil", ok, err)
	}
}

func TestOpenUnusablePathReturnsError(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err == nil {
		db.Close()
		t.Fatal("Open(dir) with a directory path: want error, got nil")
	}
}

func dbMustCreateProject(t *testing.T, db *DB, slug string) *Project {
	t.Helper()
	p, err := db.CreateProject(slug, "/repo/"+slug, "main")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return p
}

func dbMustUpsertReview(t *testing.T, db *DB, projectID int64, branch string) *Review {
	t.Helper()
	r, err := db.UpsertReview(projectID, UpsertReviewParams{
		Branch: branch, BaseBranch: "main", Title: "Title for " + branch,
		HeadSHA: "head-" + branch, BaseSHA: "base-" + branch,
	})
	if err != nil {
		t.Fatalf("UpsertReview: %v", err)
	}
	return r
}

func dbMustCreateComment(t *testing.T, db *DB, reviewID int64, body string) *Comment {
	t.Helper()
	c, err := db.CreateComment(reviewID, CreateCommentParams{Body: body})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	return c
}

func dbMustGetComment(t *testing.T, db *DB, reviewID, id int64) *Comment {
	t.Helper()
	comments, err := db.ListComments(reviewID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	for _, c := range comments {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("comment %d not found in review %d", id, reviewID)
	return nil
}

func TestClosedDatabaseSurfacesErrors(t *testing.T) {
	db := openTestDB(t)
	p := dbMustCreateProject(t, db, "proj")
	r := dbMustUpsertReview(t, db, p.ID, "b1")
	c := dbMustCreateComment(t, db, r.ID, "x")
	db.Close()

	if _, err := db.ListProjects(); err == nil {
		t.Error("ListProjects: want error on closed db")
	}
	if _, err := db.UpsertReview(p.ID, UpsertReviewParams{Branch: "b2", BaseBranch: "main"}); err == nil {
		t.Error("UpsertReview: want error on closed db")
	}
	if _, err := db.ListReviews(p.ID, "open"); err == nil {
		t.Error("ListReviews(filtered): want error on closed db")
	}
	if _, err := db.ListReviews(p.ID, ""); err == nil {
		t.Error("ListReviews(unfiltered): want error on closed db")
	}
	if _, err := db.UpdateReviewStatus(r.ID, "approved"); err == nil {
		t.Error("UpdateReviewStatus: want error on closed db")
	}
	if _, err := db.ListComments(r.ID); err == nil {
		t.Error("ListComments: want error on closed db")
	}
	if _, err := db.ListCommentsSince(r.ID, ""); err == nil {
		t.Error("ListCommentsSince: want error on closed db")
	}
	if ok, err := db.DeleteComment(r.ID, c.ID); err == nil || ok {
		t.Errorf("DeleteComment: want (false, err) on closed db, got (%v, %v)", ok, err)
	}
}

func dbSetTimestamp(t *testing.T, db *DB, table, column string, id int64, ts string) {
	t.Helper()
	if _, err := db.sql.Exec(`UPDATE `+table+` SET `+column+`=? WHERE id=?`, ts, id); err != nil {
		t.Fatalf("set %s.%s: %v", table, column, err)
	}
}

func dbReviewIDs(rs []*Review) []int64 {
	ids := make([]int64, len(rs))
	for i, r := range rs {
		ids[i] = r.ID
	}
	return ids
}

func dbCommentIDs(cs []*Comment) []int64 {
	ids := make([]int64, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}

func dbPtr[T any](v T) *T { return &v }
