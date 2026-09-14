package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"marcparadise.io/projects/reviewer/internal"
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

func newApp() *app {
	return &app{v: viper.New()}
}

func execute(t *testing.T, a *app, args ...string) (stdout, stderr *bytes.Buffer, err error) {
	t.Helper()
	root := a.rootCmd()
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	err = root.Execute()
	return
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func openDBAt(t *testing.T, dataDir string) *internal.DB {
	t.Helper()
	db, err := internal.Open(filepath.Join(dataDir, "reviewer.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func setTimestamp(t *testing.T, dataDir, table string, id int64, column, ts string) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(dataDir, "reviewer.db"))
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(fmt.Sprintf("UPDATE %s SET %s = ? WHERE id = ?", table, column), ts, id); err != nil {
		t.Fatalf("set %s.%s: %v", table, column, err)
	}
}

func newWorkRepo(t *testing.T, root, name string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	work := filepath.Join(root, name)
	runGit(t, root, "init", "-q", "-b", "main", work)
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-q", "-m", "initial")
	return work
}

func startServer(t *testing.T, dataDir string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	v := viper.New()
	v.Set("port", port)
	cfg := internal.NewFromViper(v, dataDir)
	database, err := openDB(cfg)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	srv := internal.New(cfg, database)
	go srv.ListenAndServe()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", cfg.BindAddr(), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return port
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not come up")
	return 0
}

func TestInitRegistersTheRepositoryItRunsIn(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	work := newWorkRepo(t, t.TempDir(), "my-project")
	t.Chdir(work)

	stdout, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(stdout.String(), `Registered project "my-project"`) {
		t.Errorf("stdout missing registration: %q", stdout.String())
	}

	repoPath := filepath.Join(dataDir, "repos", "my-project.git")
	if got := runGit(t, work, "remote", "get-url", "review"); got != repoPath {
		t.Errorf("review remote = %q, want %q", got, repoPath)
	}
	if !strings.Contains(stdout.String(), "Push feature branches for review") {
		t.Errorf("stdout missing next steps: %q", stdout.String())
	}

	pre, err := os.ReadFile(filepath.Join(repoPath, "hooks", "pre-receive"))
	if err != nil {
		t.Fatalf("read pre-receive: %v", err)
	}
	if !strings.Contains(string(pre), "my-project") || !strings.Contains(string(pre), "http://127.0.0.1:8080") {
		t.Errorf("pre-receive hook missing slug/URL: %s", pre)
	}
	info, err := os.Stat(filepath.Join(repoPath, "hooks", "pre-receive"))
	if err != nil {
		t.Fatalf("stat pre-receive: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("pre-receive hook not executable: %v", info.Mode())
	}

	db := openDBAt(t, dataDir)
	p, err := db.GetProject("my-project")
	if err != nil || p == nil {
		t.Fatalf("get project: %v, %v", p, err)
	}
	if p.BaseBranch != "main" {
		t.Errorf("base branch = %q, want main", p.BaseBranch)
	}
}

func TestInitTwiceIsIdempotent(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	work := newWorkRepo(t, t.TempDir(), "my-project")
	t.Chdir(work)

	if _, _, err := execute(t, newApp(), "init", "--data-dir", dataDir); err != nil {
		t.Fatalf("first init: %v", err)
	}
	stdout, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if !strings.Contains(stdout.String(), `Project "my-project" is already registered`) {
		t.Errorf("stdout missing already-registered message: %q", stdout.String())
	}

	db := openDBAt(t, dataDir)
	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 1 {
		t.Errorf("got %d projects, want 1", len(projects))
	}
}

func TestInitSlugsFromTheDirectoryName(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	work := newWorkRepo(t, t.TempDir(), "My Project")
	t.Chdir(work)

	if _, _, err := execute(t, newApp(), "init", "--data-dir", dataDir); err != nil {
		t.Fatalf("init: %v", err)
	}

	db := openDBAt(t, dataDir)
	p, err := db.GetProject("my-project")
	if err != nil || p == nil {
		t.Fatalf("get project: %v, %v", p, err)
	}
}

func TestInitFailsOutsideARepository(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	t.Chdir(t.TempDir())

	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "not in a git repository") {
		t.Fatalf("err = %v", err)
	}

	db := openDBAt(t, dataDir)
	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("registered %d projects, want none", len(projects))
	}
}

func TestInitFailsWhenTheSlugIsTakenByAnotherRepo(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	root := t.TempDir()
	first := newWorkRepo(t, filepath.Join(root, "one"), "proj")
	second := newWorkRepo(t, filepath.Join(root, "two"), "proj")

	t.Chdir(first)
	if _, _, err := execute(t, newApp(), "init", "--data-dir", dataDir); err != nil {
		t.Fatalf("first init: %v", err)
	}

	t.Chdir(second)
	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), `already registered to`) {
		t.Errorf("err = %v", err)
	}
}

func TestInitRenamesAProjectWhoseSlugDiffers(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	root := t.TempDir()

	bare := filepath.Join(root, "old-name.git")
	if err := internal.CreateBareRepo(bare); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	db := openDBAt(t, dataDir)
	if _, err := db.CreateProject("old-name", bare, "main"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	clone := filepath.Join(root, "new-name")
	runGit(t, root, "clone", "-q", bare, clone)
	t.Chdir(clone)

	stdout, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(stdout.String(), `Renamed project "old-name" to "new-name"`) {
		t.Errorf("stdout = %q", stdout.String())
	}

	if old, err := db.GetProject("old-name"); err != nil || old != nil {
		t.Errorf("old slug still registered: %+v, %v", old, err)
	}
	if renamed, err := db.GetProject("new-name"); err != nil || renamed == nil {
		t.Fatalf("renamed project missing: %+v, %v", renamed, err)
	}

	pre, err := os.ReadFile(filepath.Join(bare, "hooks", "pre-receive"))
	if err != nil {
		t.Fatalf("read pre-receive: %v", err)
	}
	if !strings.Contains(string(pre), "new-name") {
		t.Errorf("hook not rewritten with the new slug: %s", pre)
	}
}

func TestInitLeavesAConflictingReviewRemoteAlone(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	work := newWorkRepo(t, t.TempDir(), "my-project")
	runGit(t, work, "remote", "add", "review", "/somewhere/else.git")
	t.Chdir(work)

	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "already points at") {
		t.Fatalf("err = %v", err)
	}
	if got := runGit(t, work, "remote", "get-url", "review"); got != "/somewhere/else.git" {
		t.Errorf("remote was rewritten to %q", got)
	}
}

func TestInitSeedsTheBaseBranch(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	port := startServer(t, dataDir)

	work := newWorkRepo(t, t.TempDir(), "my-project")
	t.Chdir(work)

	stdout, _, err := execute(t, newApp(), "init", "--data-dir", dataDir, "--port", strconv.Itoa(port))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(stdout.String(), `Seeded the base branch "main"`) {
		t.Errorf("stdout = %q", stdout.String())
	}

	bare := filepath.Join(dataDir, "repos", "my-project.git")
	if sha := runGit(t, bare, "rev-parse", "--verify", "refs/heads/main"); sha == "" {
		t.Error("base branch was not pushed to the reviewer repo")
	}
}

func TestInitWarnsWhenTheSeedPushIsRejected(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	dead := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	work := newWorkRepo(t, t.TempDir(), "my-project")
	t.Chdir(work)

	stdout, _, err := execute(t, newApp(), "init", "--data-dir", dataDir, "--port", strconv.Itoa(dead))
	if err != nil {
		t.Fatalf("init should not fail when the server is unreachable: %v", err)
	}
	if !strings.Contains(stdout.String(), "Could not push") {
		t.Errorf("stdout = %q", stdout.String())
	}

	db := openDBAt(t, dataDir)
	if p, err := db.GetProject("my-project"); err != nil || p == nil {
		t.Errorf("project should stay registered: %+v, %v", p, err)
	}
}

func TestInitBaseBranchFlag(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	work := newWorkRepo(t, t.TempDir(), "my-project")
	t.Chdir(work)

	if _, _, err := execute(t, newApp(), "init", "--base-branch", "develop", "--data-dir", dataDir); err != nil {
		t.Fatalf("init: %v", err)
	}

	db := openDBAt(t, dataDir)
	p, err := db.GetProject("my-project")
	if err != nil || p == nil {
		t.Fatalf("get project: %v, %v", p, err)
	}
	if p.BaseBranch != "develop" {
		t.Errorf("base branch = %q, want develop", p.BaseBranch)
	}

	post, err := os.ReadFile(filepath.Join(dataDir, "repos", "my-project.git", "hooks", "post-receive"))
	if err != nil {
		t.Fatalf("read post-receive: %v", err)
	}
	if !strings.Contains(string(post), "develop") {
		t.Errorf("post-receive hook missing base branch: %s", post)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World", "hello-world"},
		{"Hello, World!!!", "hello-world"},
		{"--Foo--", "foo"},
		{"Multiple   Spaces", "multiple-spaces"},
		{"MiXeD_Case-Name", "mixed-case-name"},
		{"___", ""},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestListNoProjects(t *testing.T) {
	dataDir := t.TempDir()
	stdout, _, err := execute(t, newApp(), "list", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if stdout.String() != "No projects. Run: reviewer init\n" {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestListShowsOpenCounts(t *testing.T) {
	dataDir := t.TempDir()
	db := openDBAt(t, dataDir)

	alpha, err := db.CreateProject("alpha", "/fake/alpha", "main")
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	beta, err := db.CreateProject("beta", "/fake/beta", "main")
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}

	if _, err := db.UpsertReview(alpha.ID, internal.UpsertReviewParams{Branch: "f1", BaseBranch: "main", HeadSHA: "a", BaseSHA: "b"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := db.UpsertReview(alpha.ID, internal.UpsertReviewParams{Branch: "f2", BaseBranch: "main", HeadSHA: "a", BaseSHA: "b"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rev, err := db.UpsertReview(beta.ID, internal.UpsertReviewParams{Branch: "f3", BaseBranch: "main", HeadSHA: "a", BaseSHA: "b"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := db.UpdateReviewStatus(rev.ID, "approved"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	stdout, _, err := execute(t, newApp(), "list", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := "alpha                 (2 open)\n" +
		"  repo: /fake/alpha\n" +
		"beta                  (0 open)\n" +
		"  repo: /fake/beta\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

type repoFixture struct {
	Clone   string
	Bare    string
	Project *internal.Project
	Review  *internal.Review
}

func newRepoFixture(t *testing.T, dataDir string) *repoFixture {
	t.Helper()
	isolateGit(t)
	root := t.TempDir()

	bare := filepath.Join(root, "proj.git")
	if err := internal.CreateBareRepo(bare); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	clone := filepath.Join(root, "clone")
	runGit(t, root, "clone", "-q", bare, clone)
	runGit(t, clone, "checkout", "-q", "-b", "feat")
	if err := os.WriteFile(filepath.Join(clone, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, clone, "add", ".")
	runGit(t, clone, "commit", "-q", "-m", "feat work")

	db := openDBAt(t, dataDir)
	project, err := db.CreateProject("proj", bare, "main")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	rev, err := db.UpsertReview(project.ID, internal.UpsertReviewParams{
		Branch: "feat", BaseBranch: "main", Title: "t", HeadSHA: "a", BaseSHA: "b",
	})
	if err != nil {
		t.Fatalf("upsert review: %v", err)
	}

	t.Chdir(clone)
	return &repoFixture{Clone: clone, Bare: bare, Project: project, Review: rev}
}

func seedProject(t *testing.T, dataDir string) (*internal.Project, *internal.Review) {
	t.Helper()
	db := openDBAt(t, dataDir)
	project, err := db.CreateProject("proj", "/fake/repo", "main")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	rev, err := db.UpsertReview(project.ID, internal.UpsertReviewParams{
		Branch: "feat", BaseBranch: "main", Title: "t", HeadSHA: "a", BaseSHA: "b",
	})
	if err != nil {
		t.Fatalf("upsert review: %v", err)
	}
	return project, rev
}

func TestStatusTextOnlyShowsCommentsSinceLastOpened(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	db := openDBAt(t, dataDir)

	path := "a.go"
	before, err := db.CreateComment(f.Review.ID, internal.CreateCommentParams{Body: "before", FilePath: path})
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	atBoundary, err := db.CreateComment(f.Review.ID, internal.CreateCommentParams{Body: "at-boundary", FilePath: path})
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	after, err := db.CreateComment(f.Review.ID, internal.CreateCommentParams{Body: "after", FilePath: path})
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}

	setTimestamp(t, dataDir, "comments", before.ID, "created_at", "2019-01-01 00:00:00")
	setTimestamp(t, dataDir, "comments", atBoundary.ID, "created_at", "2020-01-01 00:00:00")
	setTimestamp(t, dataDir, "comments", after.ID, "created_at", "2021-01-01 00:00:00")
	setTimestamp(t, dataDir, "reviews", f.Review.ID, "last_opened_at", "2020-01-01 00:00:00")

	stdout, _, err := execute(t, newApp(), "status", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	out := stdout.String()
	if strings.Contains(out, "before") {
		t.Errorf("stdout should not contain comment before last_opened_at: %q", out)
	}
	if !strings.Contains(out, "at-boundary") {
		t.Errorf("stdout missing at-boundary comment: %q", out)
	}
	if !strings.Contains(out, "after") {
		t.Errorf("stdout missing after comment: %q", out)
	}
	if !strings.Contains(out, "Comments (2):") {
		t.Errorf("stdout should report 2 comments: %q", out)
	}
}

func TestStatusJSON(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	db := openDBAt(t, dataDir)
	if _, err := db.CreateComment(f.Review.ID, internal.CreateCommentParams{Body: "hello", FilePath: "a.go"}); err != nil {
		t.Fatalf("create comment: %v", err)
	}

	stdout, _, err := execute(t, newApp(), "status", "--json", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	var out struct {
		Branch   string `json:"branch"`
		Status   string `json:"status"`
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout.String())
	}
	if out.Branch != "feat" || out.Status != "open" {
		t.Errorf("got branch=%q status=%q", out.Branch, out.Status)
	}
	if len(out.Comments) != 1 || out.Comments[0].Body != "hello" {
		t.Errorf("comments = %+v", out.Comments)
	}
}

func TestStatusUnknownBranch(t *testing.T) {
	dataDir := t.TempDir()
	newRepoFixture(t, dataDir)

	_, _, err := execute(t, newApp(), "status", "nope", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), `no review for branch "nope"`) {
		t.Errorf("err = %v", err)
	}
}

func TestStatusResolvesProjectAndBranchFromRepo(t *testing.T) {
	dataDir := t.TempDir()
	newRepoFixture(t, dataDir)

	stdout, _, err := execute(t, newApp(), "status", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout.String(), "Branch:  feat") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestStatusResolvesThroughRemoteOtherThanOrigin(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	runGit(t, f.Clone, "remote", "rename", "origin", "reviewer")
	runGit(t, f.Clone, "remote", "add", "origin", "https://example.com/somewhere.git")

	stdout, _, err := execute(t, newApp(), "status", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout.String(), "Branch:  feat") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestStatusResolvesBranchOfAnEmptyClone(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	root := t.TempDir()

	barePath := filepath.Join(root, "reviewer.git")
	if err := internal.CreateBareRepo(barePath); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	db := openDBAt(t, dataDir)
	if _, err := db.CreateProject("proj", barePath, "main"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	clone := filepath.Join(root, "clone")
	runGit(t, root, "clone", "-q", barePath, clone)

	t.Chdir(clone)
	_, _, err := execute(t, newApp(), "status", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "no review for branch") {
		t.Errorf("err = %v, want the unborn branch resolved by name", err)
	}
}

func TestStatusFailsWhenRemotesMatchTwoProjects(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	other := filepath.Join(t.TempDir(), "other.git")
	if err := internal.CreateBareRepo(other); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	db := openDBAt(t, dataDir)
	if _, err := db.CreateProject("other", other, "main"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	runGit(t, f.Clone, "remote", "add", "other", other)

	_, _, err := execute(t, newApp(), "status", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "remove or rename all but one") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "proj") || !strings.Contains(err.Error(), "other") {
		t.Errorf("err should name both projects: %v", err)
	}
}

func TestStatusResolvesFileURLRemote(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	runGit(t, f.Clone, "remote", "set-url", "origin", "file://"+f.Bare)

	stdout, _, err := execute(t, newApp(), "status", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout.String(), "Branch:  feat") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestTooManyArguments(t *testing.T) {
	dataDir := t.TempDir()
	for _, name := range []string{"status", "wait", "response-block"} {
		t.Run(name, func(t *testing.T) {
			_, _, err := execute(t, newApp(), name, "feat", "extra", "--data-dir", dataDir)
			if err == nil || !strings.Contains(err.Error(), "at most 1 argument") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestResponseBlockResolvesBranchFromRepo(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	db := openDBAt(t, dataDir)
	if _, err := db.UpdateReviewStatus(f.Review.ID, "changes_requested"); err != nil {
		t.Fatalf("update status: %v", err)
	}
	if _, err := db.CreateComment(f.Review.ID, internal.CreateCommentParams{Body: "please fix", FilePath: "a.go"}); err != nil {
		t.Fatalf("create comment: %v", err)
	}

	stdout, _, err := execute(t, newApp(), "response-block", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("response-block: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Review-Response: proj feat") || !strings.Contains(out, "please fix") {
		t.Errorf("stdout = %q", out)
	}
}

func TestResponseBlockStaysSilentOutsideReviewerRepo(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	seedProject(t, dataDir)

	root := t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main", root)
	runGit(t, root, "remote", "add", "origin", "https://example.com/elsewhere.git")
	t.Chdir(root)

	stdout, _, err := execute(t, newApp(), "response-block", "--data-dir", dataDir)
	if err != nil || stdout.String() != "" {
		t.Errorf("err=%v stdout=%q", err, stdout.String())
	}
}

func TestStatusExplicitBranchOverridesCurrentBranch(t *testing.T) {
	dataDir := t.TempDir()
	newRepoFixture(t, dataDir)

	_, _, err := execute(t, newApp(), "status", "main", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), `no review for branch "main"`) {
		t.Errorf("err = %v", err)
	}
}

func TestStatusFailsOutsideRepo(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	seedProject(t, dataDir)

	t.Chdir(t.TempDir())
	_, _, err := execute(t, newApp(), "status", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "not inside a git repository") {
		t.Errorf("err = %v", err)
	}
}

func TestStatusFailsOnDetachedHead(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	runGit(t, f.Clone, "checkout", "-q", "--detach")

	_, _, err := execute(t, newApp(), "status", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "could not determine the current branch") {
		t.Errorf("err = %v", err)
	}
}

func TestWaitReturnsImmediatelyWhenNotOpen(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	db := openDBAt(t, dataDir)
	if _, err := db.UpdateReviewStatus(f.Review.ID, "changes_requested"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	a := &app{v: viper.New(), pollInterval: 5 * time.Millisecond}
	stdout, _, err := execute(t, a, "wait", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !strings.Contains(stdout.String(), "Status:  changes_requested") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestWaitPicksUpPollingChange(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	updater := openDBAt(t, dataDir)

	go func() {
		time.Sleep(30 * time.Millisecond)
		updater.UpdateReviewStatus(f.Review.ID, "approved")
	}()

	a := &app{v: viper.New(), pollInterval: 5 * time.Millisecond}
	type result struct {
		stdout *bytes.Buffer
		err    error
	}
	done := make(chan result, 1)
	go func() {
		stdout, _, err := execute(t, a, "wait", "--data-dir", dataDir)
		done <- result{stdout, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("wait: %v", r.err)
		}
		if !strings.Contains(r.stdout.String(), "Status:  approved") {
			t.Errorf("stdout = %q", r.stdout.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not pick up the status change in time")
	}
}

func TestWaitTimeout(t *testing.T) {
	dataDir := t.TempDir()
	newRepoFixture(t, dataDir)

	a := &app{v: viper.New(), pollInterval: 5 * time.Millisecond}
	_, _, err := execute(t, a, "wait", "--timeout", "20ms", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for review of") {
		t.Errorf("err = %v", err)
	}
}

func TestResponseBlockPrintsForChangesRequestedWithComments(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	db := openDBAt(t, dataDir)
	if _, err := db.UpdateReviewStatus(f.Review.ID, "changes_requested"); err != nil {
		t.Fatalf("update status: %v", err)
	}
	if _, err := db.CreateComment(f.Review.ID, internal.CreateCommentParams{Body: "please fix", FilePath: "a.go"}); err != nil {
		t.Fatalf("create comment: %v", err)
	}

	stdout, _, err := execute(t, newApp(), "response-block", "feat", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("response-block: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Review-Response: proj feat") {
		t.Errorf("stdout missing header: %q", out)
	}
	if !strings.Contains(out, "please fix") {
		t.Errorf("stdout missing comment body: %q", out)
	}
}

func TestResponseBlockSilentCases(t *testing.T) {
	t.Run("unknown branch", func(t *testing.T) {
		dataDir := t.TempDir()
		newRepoFixture(t, dataDir)
		stdout, _, err := execute(t, newApp(), "response-block", "nope", "--data-dir", dataDir)
		if err != nil || stdout.String() != "" {
			t.Errorf("err=%v stdout=%q", err, stdout.String())
		}
	})
	t.Run("other status", func(t *testing.T) {
		dataDir := t.TempDir()
		f := newRepoFixture(t, dataDir)
		db := openDBAt(t, dataDir)
		if _, err := db.CreateComment(f.Review.ID, internal.CreateCommentParams{Body: "x", FilePath: "a.go"}); err != nil {
			t.Fatalf("create comment: %v", err)
		}
		stdout, _, err := execute(t, newApp(), "response-block", "--data-dir", dataDir)
		if err != nil || stdout.String() != "" {
			t.Errorf("err=%v stdout=%q", err, stdout.String())
		}
	})
	t.Run("no comments", func(t *testing.T) {
		dataDir := t.TempDir()
		f := newRepoFixture(t, dataDir)
		db := openDBAt(t, dataDir)
		if _, err := db.UpdateReviewStatus(f.Review.ID, "changes_requested"); err != nil {
			t.Fatalf("update status: %v", err)
		}
		stdout, _, err := execute(t, newApp(), "response-block", "--data-dir", dataDir)
		if err != nil || stdout.String() != "" {
			t.Errorf("err=%v stdout=%q", err, stdout.String())
		}
	})
}

func TestInstallHooksRewritesPort(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()

	work := newWorkRepo(t, t.TempDir(), "my-project")
	t.Chdir(work)

	if _, _, err := execute(t, newApp(), "init", "--data-dir", dataDir); err != nil {
		t.Fatalf("init: %v", err)
	}

	if _, _, err := execute(t, newApp(), "install-hooks", "--port", "9999", "--data-dir", dataDir); err != nil {
		t.Fatalf("install-hooks: %v", err)
	}

	pre, err := os.ReadFile(filepath.Join(dataDir, "repos", "my-project.git", "hooks", "pre-receive"))
	if err != nil {
		t.Fatalf("read pre-receive: %v", err)
	}
	if !strings.Contains(string(pre), "9999") {
		t.Errorf("hook missing new port: %s", pre)
	}
	if strings.Contains(string(pre), "8080") {
		t.Errorf("hook still has old port: %s", pre)
	}
}

func TestInstallHooksReportsBrokenRepoButContinues(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()

	root := t.TempDir()
	good := newWorkRepo(t, root, "good-project")
	broken := newWorkRepo(t, root, "broken-project")

	t.Chdir(good)
	if _, _, err := execute(t, newApp(), "init", "--data-dir", dataDir); err != nil {
		t.Fatalf("init good: %v", err)
	}
	t.Chdir(broken)
	if _, _, err := execute(t, newApp(), "init", "--data-dir", dataDir); err != nil {
		t.Fatalf("init broken: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(dataDir, "repos", "broken-project.git")); err != nil {
		t.Fatalf("remove repo: %v", err)
	}

	stdout, stderr, err := execute(t, newApp(), "install-hooks", "--port", "12345", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("install-hooks: %v", err)
	}
	if !strings.Contains(stderr.String(), "error installing hook for broken-project") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "installed hook for good-project") {
		t.Errorf("stdout = %q", stdout.String())
	}

	pre, err := os.ReadFile(filepath.Join(dataDir, "repos", "good-project.git", "hooks", "pre-receive"))
	if err != nil {
		t.Fatalf("read pre-receive: %v", err)
	}
	if !strings.Contains(string(pre), "12345") {
		t.Errorf("good project's hook not rewritten: %s", pre)
	}
}

func TestCommentLocation(t *testing.T) {
	path := "a/b.go"
	var ln, le int64 = 10, 12

	cases := []struct {
		name string
		c    *internal.Comment
		want string
	}{
		{"general", &internal.Comment{}, "(general)"},
		{"file only", &internal.Comment{FilePath: &path}, "a/b.go"},
		{"single line", &internal.Comment{FilePath: &path, LineNumber: &ln}, "a/b.go:10"},
		{"range", &internal.Comment{FilePath: &path, LineNumber: &ln, LineEnd: &le}, "a/b.go:10-12"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := commentLocation(c.c); got != c.want {
				t.Errorf("commentLocation() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestServeReturnsErrorWhenPortInUse(t *testing.T) {
	dataDir := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	_, _, err = execute(t, newApp(), "serve",
		"--port", fmt.Sprintf("%d", port),
		"--resync-interval", "0",
		"--data-dir", dataDir,
	)
	if err == nil {
		t.Fatal("expected an error from serve when the port is already bound")
	}
}

func TestDefaultDataDirUsedWithoutFlag(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	root := newRootCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs([]string{"list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}

	want := filepath.Join(xdg, "reviewer")
	if _, err := os.Stat(filepath.Join(want, "reviewer.db")); err != nil {
		t.Errorf("db not created at default location: %v", err)
	}
	if info, err := os.Stat(filepath.Join(want, "repos")); err != nil || !info.IsDir() {
		t.Errorf("repos dir not created at default location: %v", err)
	}
}

func unusableDataDirCommandCases() [][]string {
	return [][]string{
		{"serve", "--resync-interval", "0"},
		{"init"},
		{"list"},
		{"status"},
		{"wait"},
		{"response-block"},
		{"install-hooks"},
	}
}

func TestCommandsReportUnusableDataDir(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	for _, args := range unusableDataDirCommandCases() {
		t.Run(args[0], func(t *testing.T) {
			_, _, err := execute(t, newApp(), append(args, "--data-dir", blocked)...)
			if err == nil || !strings.Contains(err.Error(), "creating data dirs") {
				t.Errorf("err = %v, want containing %q", err, "creating data dirs")
			}
		})
	}

}

func TestCommandsReportUnopenableDatabase(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "reviewer.db"), 0o755); err != nil {
		t.Fatalf("make reviewer.db a directory: %v", err)
	}

	for _, args := range unusableDataDirCommandCases() {
		t.Run(args[0], func(t *testing.T) {
			_, _, err := execute(t, newApp(), append(args, "--data-dir", dataDir)...)
			if err == nil || !strings.Contains(err.Error(), "opening database") {
				t.Errorf("err = %v, want containing %q", err, "opening database")
			}
		})
	}

}

func TestWaitUnknownBranch(t *testing.T) {
	dataDir := t.TempDir()
	newRepoFixture(t, dataDir)
	a := &app{v: viper.New(), pollInterval: 5 * time.Millisecond}

	_, _, err := execute(t, a, "wait", "nope", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), `no review for branch "nope"`) {
		t.Errorf("err = %v", err)
	}
}

func TestInitFailsWithoutRegisteringWhenRepoPathBlocked(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	reposDir := filepath.Join(dataDir, "repos")
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		t.Fatalf("mkdir repos: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reposDir, "my-project.git"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	work := newWorkRepo(t, t.TempDir(), "my-project")
	t.Chdir(work)
	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "creating bare repo") {
		t.Errorf("err = %v, want containing %q", err, "creating bare repo")
	}

	db := openDBAt(t, dataDir)
	p, err := db.GetProject("my-project")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p != nil {
		t.Errorf("project should not be registered after failed init, got %+v", p)
	}
}

func TestInitFailsWhenTheDirectoryNameHasNoSlug(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	work := newWorkRepo(t, t.TempDir(), "!!!")

	t.Chdir(work)
	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "cannot name a project after the directory") {
		t.Errorf("err = %v", err)
	}
}

func TestInitRegistersNothingWhenTheHookCannotBeInstalled(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	work := newWorkRepo(t, t.TempDir(), "proj")

	repoPath := filepath.Join(dataDir, "repos", "proj.git")
	if err := internal.CreateBareRepo(repoPath); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	blockHook(t, repoPath)

	t.Chdir(work)
	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "installing hook") {
		t.Errorf("err = %v", err)
	}

	p, err := openDBAt(t, dataDir).GetProject("proj")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p != nil {
		t.Errorf("project registered despite the hook failure: %+v", p)
	}
}

func TestInitReportsAHookFailureWhenRenaming(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	root := t.TempDir()

	bare := filepath.Join(root, "old-name.git")
	if err := internal.CreateBareRepo(bare); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	blockHook(t, bare)
	db := openDBAt(t, dataDir)
	if _, err := db.CreateProject("old-name", bare, "main"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	clone := filepath.Join(root, "new-name")
	runGit(t, root, "clone", "-q", bare, clone)
	t.Chdir(clone)

	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "installing hook") {
		t.Errorf("err = %v", err)
	}
}

func TestInitRefusesARenameWhenTheDirectoryNameIsTaken(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	root := t.TempDir()

	oldBare := filepath.Join(root, "old-name.git")
	otherBare := filepath.Join(root, "new-name.git")
	for _, path := range []string{oldBare, otherBare} {
		if err := internal.CreateBareRepo(path); err != nil {
			t.Fatalf("create bare repo %s: %v", path, err)
		}
	}
	db := openDBAt(t, dataDir)
	if _, err := db.CreateProject("old-name", oldBare, "main"); err != nil {
		t.Fatalf("create old-name: %v", err)
	}
	if _, err := db.CreateProject("new-name", otherBare, "main"); err != nil {
		t.Fatalf("create new-name: %v", err)
	}

	clone := filepath.Join(root, "new-name")
	runGit(t, root, "clone", "-q", oldBare, clone)
	t.Chdir(clone)

	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "this repository is registered as") {
		t.Errorf("err = %v", err)
	}
}

func blockHook(t *testing.T, repoPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repoPath, "hooks", "pre-receive"), 0o755); err != nil {
		t.Fatalf("mkdir hook path: %v", err)
	}
}

func TestStatusFailsWhenTheRepoHasNoRemotes(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	work := newWorkRepo(t, t.TempDir(), "proj")

	t.Chdir(work)
	_, _, err := execute(t, newApp(), "status", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "it has no git remotes") {
		t.Errorf("err = %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "reviewer init") {
		t.Errorf("err should point at init: %v", err)
	}
}

func TestWaitFailsOutsideAGitRepository(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	seedProject(t, dataDir)
	t.Chdir(t.TempDir())

	_, _, err := execute(t, newApp(), "wait", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "not inside a git repository") {
		t.Errorf("err = %v", err)
	}
}

func TestWaitFailsOnDetachedHead(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	runGit(t, f.Clone, "checkout", "-q", "--detach")

	a := &app{v: viper.New(), pollInterval: 5 * time.Millisecond}
	_, _, err := execute(t, a, "wait", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "could not determine the current branch") {
		t.Errorf("err = %v", err)
	}
}

func TestResponseBlockReportsAmbiguousRemotes(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)

	other := filepath.Join(t.TempDir(), "other.git")
	if err := internal.CreateBareRepo(other); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	db := openDBAt(t, dataDir)
	if _, err := db.CreateProject("other", other, "main"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	runGit(t, f.Clone, "remote", "add", "other", other)

	_, _, err := execute(t, newApp(), "response-block", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "remove or rename all but one") {
		t.Errorf("err = %v", err)
	}
}

func TestResponseBlockStaysSilentOnDetachedHead(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)
	runGit(t, f.Clone, "checkout", "-q", "--detach")

	stdout, _, err := execute(t, newApp(), "response-block", "--data-dir", dataDir)
	if err != nil || stdout.String() != "" {
		t.Errorf("err=%v stdout=%q", err, stdout.String())
	}
}

func TestCanonicalPathHandlesEmptyAndFileURLs(t *testing.T) {
	if got := canonicalPath(""); got != "" {
		t.Errorf("canonicalPath(%q) = %q, want empty", "", got)
	}

	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got := canonicalPath("file://" + dir); got != resolved {
		t.Errorf("canonicalPath(file://%s) = %q, want %q", dir, got, resolved)
	}
}

func TestProjectAtPathIgnoresAnEmptyRemoteURL(t *testing.T) {
	project := &internal.Project{ID: 1, Slug: "proj", RepoPath: t.TempDir()}
	projects := []*internal.Project{project}

	if got := projectAtPath(projects, ""); got != nil {
		t.Errorf("projectAtPath(%q) = %+v, want nil", "", got)
	}
	if got := projectAtPath(projects, project.RepoPath); got != project {
		t.Errorf("projectAtPath(%s) = %+v, want the matching project", project.RepoPath, got)
	}
}

func TestCanonicalPathFallsBackWhenTheWorkingDirectoryIsGone(t *testing.T) {
	gone := t.TempDir()
	t.Chdir(gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatalf("remove working directory: %v", err)
	}

	if got := canonicalPath("relative/path"); got != "relative/path" {
		t.Errorf("canonicalPath(%q) with no working directory = %q, want %q", "relative/path", got, "relative/path")
	}
}

func TestInitFailsInsideABareRepository(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	bare := filepath.Join(t.TempDir(), "bare.git")
	if err := internal.CreateBareRepo(bare); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}

	t.Chdir(bare)
	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "reading this repository's location") {
		t.Errorf("err = %v", err)
	}
}

func TestInitFailsWhenRemotesMatchTwoProjects(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)

	other := filepath.Join(t.TempDir(), "other.git")
	if err := internal.CreateBareRepo(other); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	db := openDBAt(t, dataDir)
	if _, err := db.CreateProject("other", other, "main"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	runGit(t, f.Clone, "remote", "add", "other", other)

	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "remove or rename all but one") {
		t.Errorf("err = %v", err)
	}
}

func TestRepoRemotesSkipsARemoteWithoutAURL(t *testing.T) {
	dataDir := t.TempDir()
	f := newRepoFixture(t, dataDir)

	config := filepath.Join(f.Clone, ".git", "config")
	broken := "[remote \"broken\"]\n\tfetch = +refs/heads/*:refs/remotes/broken/*\n"
	existing, err := os.ReadFile(config)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if err := os.WriteFile(config, append(existing, broken...), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if out := runGit(t, f.Clone, "remote"); !strings.Contains(out, "broken") {
		t.Fatalf("git remote = %q, want it to list broken", out)
	}

	stdout, _, err := execute(t, newApp(), "status", "--data-dir", dataDir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout.String(), "Branch:  feat") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestInitReportsAProjectWriteFailure(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	work := newWorkRepo(t, t.TempDir(), "proj")
	openDBAt(t, dataDir)
	execRaw(t, dataDir, "CREATE TRIGGER no_insert BEFORE INSERT ON projects BEGIN SELECT RAISE(ABORT, 'blocked'); END")

	t.Chdir(work)
	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "creating project") {
		t.Errorf("err = %v", err)
	}
}

func TestInitReportsARenameWriteFailure(t *testing.T) {
	isolateGit(t)
	dataDir := t.TempDir()
	root := t.TempDir()

	bare := filepath.Join(root, "old-name.git")
	if err := internal.CreateBareRepo(bare); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	db := openDBAt(t, dataDir)
	if _, err := db.CreateProject("old-name", bare, "main"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	execRaw(t, dataDir, "CREATE TRIGGER no_update BEFORE UPDATE ON projects BEGIN SELECT RAISE(ABORT, 'blocked'); END")

	clone := filepath.Join(root, "new-name")
	runGit(t, root, "clone", "-q", bare, clone)
	t.Chdir(clone)

	_, _, err := execute(t, newApp(), "init", "--data-dir", dataDir)
	if err == nil || !strings.Contains(err.Error(), "renaming project") {
		t.Errorf("err = %v", err)
	}
}

func execRaw(t *testing.T, dataDir, stmt string) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(dataDir, "reviewer.db"))
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(stmt); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}
