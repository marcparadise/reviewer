package internal

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// stubServer builds a Server with a fixed port and no DB, for exercising the
// guard middleware in isolation.
func stubServer() *Server {
	return &Server{cfg: &Config{Port: 10001}}
}

// dbServer builds a Server backed by a fresh temp SQLite database.
func dbServer(t *testing.T) *Server {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return New(&Config{Port: 10001, DataDir: t.TempDir()}, db)
}

func TestGuard(t *testing.T) {
	s := stubServer()
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.guard(ok)

	cases := []struct {
		name        string
		method      string
		host        string
		origin      string
		contentType string
		body        string
		want        int
	}{
		{"get loopback host", "GET", "127.0.0.1:10001", "", "", "", 200},
		{"get localhost host", "GET", "localhost:10001", "", "", "", 200},
		{"get foreign host rejected", "GET", "evil.com", "", "", "", 403},
		{"get loopback wrong port rejected", "GET", "127.0.0.1:9999", "", "", "", 403},
		{"post no origin json ok (hook/cli)", "POST", "127.0.0.1:10001", "", "application/json", `{"x":1}`, 200},
		{"post same origin ok", "POST", "127.0.0.1:10001", "http://127.0.0.1:10001", "application/json", `{"x":1}`, 200},
		{"post foreign origin rejected", "POST", "127.0.0.1:10001", "http://evil.com", "application/json", `{"x":1}`, 403},
		{"post malformed origin rejected", "POST", "127.0.0.1:10001", "http://[::1", "application/json", `{"x":1}`, 403},
		{"post origin missing host rejected", "POST", "127.0.0.1:10001", "http://", "application/json", `{"x":1}`, 403},
		{"post non-json body rejected", "POST", "127.0.0.1:10001", "", "text/plain", `{"x":1}`, 415},
		{"delete bodyless ok", "DELETE", "127.0.0.1:10001", "", "", "", 200},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var bodyReader *strings.Reader
			if c.body != "" {
				bodyReader = strings.NewReader(c.body)
			} else {
				bodyReader = strings.NewReader("")
			}
			req := httptest.NewRequest(c.method, "http://"+c.host+"/api/x", bodyReader)
			req.Host = c.host
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			if c.contentType != "" {
				req.Header.Set("Content-Type", c.contentType)
			}
			// A bodyless request must report ContentLength 0 so the guard skips
			// the content-type check (mirrors the UI's DELETE requests).
			if c.body == "" {
				req.ContentLength = 0
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
		})
	}
}

func TestGuardNoCORSHeader(t *testing.T) {
	s := stubServer()
	h := s.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "http://127.0.0.1:10001/api/x", nil)
	req.Host = "127.0.0.1:10001"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Fatalf("unexpected CORS header: %q", v)
	}
}

func TestDeleteCommentAuthorization(t *testing.T) {
	s := dbServer(t)
	p, err := s.db.CreateProject("proj", filepath.Join(t.TempDir(), "p.git"), "main")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	revA, err := s.db.UpsertReview(p.ID, UpsertReviewParams{Branch: "a", BaseBranch: "main", Title: "a", HeadSHA: "aaa", BaseSHA: "000"})
	if err != nil {
		t.Fatalf("upsert review a: %v", err)
	}
	revB, err := s.db.UpsertReview(p.ID, UpsertReviewParams{Branch: "b", BaseBranch: "main", Title: "b", HeadSHA: "bbb", BaseSHA: "000"})
	if err != nil {
		t.Fatalf("upsert review b: %v", err)
	}
	c, err := s.db.CreateComment(revA.ID, CreateCommentParams{Body: "hi"})
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}

	handler := s.Handler()
	del := func(reviewID, commentID int64) int {
		url := "http://127.0.0.1:10001/api/projects/proj/reviews/" +
			strconv.FormatInt(reviewID, 10) + "/comments/" + strconv.FormatInt(commentID, 10)
		req := httptest.NewRequest("DELETE", url, nil)
		req.Host = "127.0.0.1:10001"
		req.ContentLength = 0
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Deleting comment c through review B's path must not touch it.
	if code := del(revB.ID, c.ID); code != http.StatusNotFound {
		t.Fatalf("cross-review delete: status = %d, want 404", code)
	}
	remaining, _ := s.db.ListComments(revA.ID)
	if len(remaining) != 1 {
		t.Fatalf("comment was deleted through the wrong review; %d remain, want 1", len(remaining))
	}

	// Deleting through its own review succeeds.
	if code := del(revA.ID, c.ID); code != http.StatusOK {
		t.Fatalf("owning-review delete: status = %d, want 200", code)
	}
	remaining, _ = s.db.ListComments(revA.ID)
	if len(remaining) != 0 {
		t.Fatalf("comment survived its own-review delete; %d remain, want 0", len(remaining))
	}
}

func TestUpdateReviewStatusTransitions(t *testing.T) {
	s := dbServer(t)
	p, err := s.db.CreateProject("proj", filepath.Join(t.TempDir(), "p.git"), "main")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	rev, err := s.db.UpsertReview(p.ID, UpsertReviewParams{Branch: "feat", BaseBranch: "main", Title: "feat", HeadSHA: "aaa", BaseSHA: "000"})
	if err != nil {
		t.Fatalf("upsert review: %v", err)
	}

	handler := s.Handler()
	patch := func(body string) int {
		url := "http://127.0.0.1:10001/api/projects/proj/reviews/" + strconv.FormatInt(rev.ID, 10)
		req := httptest.NewRequest("PATCH", url, strings.NewReader(body))
		req.Host = "127.0.0.1:10001"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	cases := []struct {
		body string
		want int
	}{
		{`{"status":"open"}`, http.StatusBadRequest},
		{`{"status":"closed"}`, http.StatusBadRequest},
		{`{"status":"bogus"}`, http.StatusBadRequest},
		{`{"status":"approved"}`, http.StatusOK},
		{`{"status":"changes_requested"}`, http.StatusOK},
		{`{"status":"paused"}`, http.StatusOK},
		{`{}`, http.StatusOK}, // no status field
	}
	for _, c := range cases {
		if code := patch(c.body); code != c.want {
			t.Fatalf("PATCH %s: status = %d, want %d", c.body, code, c.want)
		}
	}
}
