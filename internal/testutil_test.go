package internal

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test Author")
	t.Setenv("GIT_AUTHOR_EMAIL", "author@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test Author")
	t.Setenv("GIT_COMMITTER_EMAIL", "author@example.com")
}

type testRepo struct {
	t    *testing.T
	Bare string
	Work string
	tick int
}

var testEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	isolateGit(t)
	dir := t.TempDir()
	r := &testRepo{t: t, Bare: filepath.Join(dir, "origin.git"), Work: filepath.Join(dir, "work")}
	if err := CreateBareRepo(r.Bare); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	r.run(r.Bare, "symbolic-ref", "HEAD", "refs/heads/main")
	r.run("", "init", "-q", "-b", "main", r.Work)
	r.run(r.Work, "remote", "add", "origin", r.Bare)
	return r
}

func (r *testRepo) run(dir string, args ...string) string {
	r.t.Helper()
	r.tick++
	date := testEpoch.Add(time.Duration(r.tick) * time.Minute).Format(time.RFC3339)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *testRepo) Git(args ...string) string {
	r.t.Helper()
	return r.run(r.Work, args...)
}

func (r *testRepo) Write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.Work, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *testRepo) Commit(path, content, msg string) string {
	r.t.Helper()
	r.Write(path, content)
	r.Git("add", "-A")
	r.Git("commit", "-q", "-m", msg)
	return r.SHA("HEAD")
}

func (r *testRepo) Remove(path, msg string) string {
	r.t.Helper()
	r.Git("rm", "-q", path)
	r.Git("commit", "-q", "-m", msg)
	return r.SHA("HEAD")
}

func (r *testRepo) Branch(name string) {
	r.t.Helper()
	r.Git("checkout", "-q", "-b", name)
}

func (r *testRepo) Checkout(name string) {
	r.t.Helper()
	r.Git("checkout", "-q", name)
}

func (r *testRepo) MergeNoFF(branch string) string {
	r.t.Helper()
	r.Git("merge", "-q", "--no-ff", "-m", "Merge branch '"+branch+"'", branch)
	return r.SHA("HEAD")
}

func (r *testRepo) Push(refspecs ...string) {
	r.t.Helper()
	r.Git(append([]string{"push", "-q", "origin"}, refspecs...)...)
}

func (r *testRepo) SHA(ref string) string {
	r.t.Helper()
	return r.Git("rev-parse", ref)
}

const testPort = 10001

type fixture struct {
	*testRepo
	db      *DB
	srv     *Server
	project *Project
	handler http.Handler
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	repo := newTestRepo(t)
	db := openTestDB(t)
	srv := New(&Config{Port: testPort, DataDir: t.TempDir()}, db)
	p, err := db.CreateProject("proj", repo.Bare, "main")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return &fixture{testRepo: repo, db: db, srv: srv, project: p, handler: srv.Handler()}
}

func testDo(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", testPort, path), reader)
	req.Host = fmt.Sprintf("127.0.0.1:%d", testPort)
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) do(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	return testDo(f.t, f.handler, method, path, body)
}

func (f *fixture) postReceive(branch string) *Review {
	f.t.Helper()
	rec := f.do("POST", "/api/hooks/post-receive", map[string]string{
		"project_slug": f.project.Slug,
		"branch":       branch,
		"new_sha":      f.SHA(branch),
	})
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("post-receive %s: status %d: %s", branch, rec.Code, rec.Body)
	}
	return decode[*Review](f.t, rec)
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %T from %q: %v", v, rec.Body.String(), err)
	}
	return v
}
