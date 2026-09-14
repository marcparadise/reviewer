package internal

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestGetFileContentPreservesLeadingWhitespaceAndLineNumbers(t *testing.T) {
	r := newTestRepo(t)
	sha := r.Commit("f.go", "\n\n    indented\nlast\n\n", "add file")

	got, err := GetFileContent(r.Work, sha, "f.go")
	if err != nil {
		t.Fatalf("GetFileContent: %v", err)
	}
	want := []string{"", "", "    indented", "last", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

func TestGetFileContentEmptyFileHasNoLines(t *testing.T) {
	r := newTestRepo(t)
	sha := r.Commit("empty.txt", "", "add empty file")

	got, err := GetFileContent(r.Work, sha, "empty.txt")
	if err != nil {
		t.Fatalf("GetFileContent: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("lines = %q, want none", got)
	}
}

func TestGetFileContentMissingPathIsError(t *testing.T) {
	r := newTestRepo(t)
	sha := r.Commit("a.txt", "content\n", "add a")

	if _, err := GetFileContent(r.Work, sha, "nope.txt"); err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestCreateBareRepoCreatesMissingParentDirs(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "repo.git")

	if err := CreateBareRepo(path); err != nil {
		t.Fatalf("CreateBareRepo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err != nil {
		t.Fatalf("expected bare repo at %s: %v", path, err)
	}
}

func TestInstallHookWritesExecutableHooksWithContent(t *testing.T) {
	r := newTestRepo(t)

	if err := InstallHook(r.Bare, "http://127.0.0.1:9999", "proj", "main"); err != nil {
		t.Fatalf("InstallHook: %v", err)
	}

	pre, err := os.ReadFile(filepath.Join(r.Bare, "hooks", "pre-receive"))
	if err != nil {
		t.Fatalf("read pre-receive: %v", err)
	}
	if !strings.Contains(string(pre), "http://127.0.0.1:9999") || !strings.Contains(string(pre), "proj") {
		t.Fatalf("pre-receive missing URL or slug: %s", pre)
	}

	post, err := os.ReadFile(filepath.Join(r.Bare, "hooks", "post-receive"))
	if err != nil {
		t.Fatalf("read post-receive: %v", err)
	}
	if !strings.Contains(string(post), "http://127.0.0.1:9999") || !strings.Contains(string(post), "proj") || !strings.Contains(string(post), "main") {
		t.Fatalf("post-receive missing URL, slug, or base branch: %s", post)
	}

	for _, name := range []string{"pre-receive", "post-receive"} {
		info, err := os.Stat(filepath.Join(r.Bare, "hooks", name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("%s is not executable: %v", name, info.Mode())
		}
	}
}

func TestGetRemoteURL(t *testing.T) {
	r := newTestRepo(t)
	t.Chdir(r.Work)

	got, err := GetRemoteURL("origin")
	if err != nil {
		t.Fatalf("GetRemoteURL: %v", err)
	}
	if got != r.Bare {
		t.Errorf("GetRemoteURL(origin) = %s, want %s", got, r.Bare)
	}

	if _, err := GetRemoteURL("nope"); err == nil {
		t.Fatal("expected error for unknown remote")
	}
}

func TestInGitRepoIsTrueInsideARepoAndFalseOutsideOne(t *testing.T) {
	r := newTestRepo(t)
	t.Chdir(r.Work)

	if !InGitRepo() {
		t.Error("InGitRepo() = false inside a work tree, want true")
	}

	t.Chdir(t.TempDir())
	if InGitRepo() {
		t.Error("InGitRepo() = true outside any repo, want false")
	}
}

func TestRepoTopLevelReturnsTheWorkTreeRoot(t *testing.T) {
	r := newTestRepo(t)
	t.Chdir(r.Work)

	got, err := RepoTopLevel()
	if err != nil {
		t.Fatalf("RepoTopLevel: %v", err)
	}
	if !sameDir(t, got, r.Work) {
		t.Errorf("RepoTopLevel() = %s, want %s", got, r.Work)
	}

	deep := filepath.Join(r.Work, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Chdir(deep)
	if got, err = RepoTopLevel(); err != nil {
		t.Fatalf("RepoTopLevel from a subdirectory: %v", err)
	}
	if !sameDir(t, got, r.Work) {
		t.Errorf("RepoTopLevel() from %s = %s, want %s", deep, got, r.Work)
	}
}

func sameDir(t *testing.T, got, want string) bool {
	t.Helper()
	g, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", got, err)
	}
	w, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", want, err)
	}
	return g == w
}

func TestAddRemoteAddsOneAndRejectsADuplicate(t *testing.T) {
	r := newTestRepo(t)
	t.Chdir(r.Work)
	url := filepath.Join(t.TempDir(), "other.git")

	if err := AddRemote("extra", url); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	got, err := GetRemoteURL("extra")
	if err != nil {
		t.Fatalf("GetRemoteURL(extra): %v", err)
	}
	if got != url {
		t.Errorf("GetRemoteURL(extra) = %s, want %s", got, url)
	}

	if err := AddRemote("extra", url); err == nil {
		t.Error("AddRemote with an existing name = nil, want error")
	}
}

func TestPushPublishesABranchToTheRemote(t *testing.T) {
	r := newTestRepo(t)
	t.Chdir(r.Work)
	sha := r.Commit("a.txt", "one\n", "initial")

	if err := Push("origin", "main"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := r.run(r.Bare, "rev-parse", "refs/heads/main"); got != sha {
		t.Errorf("origin main = %s, want %s", got, sha)
	}

	if err := Push("origin", "nope"); err == nil {
		t.Error("Push of an unknown branch = nil, want error")
	}
}

func TestRemoteNamesListsRemotesAndIsEmptyWithoutAny(t *testing.T) {
	r := newTestRepo(t)
	t.Chdir(r.Work)

	names, err := RemoteNames()
	if err != nil {
		t.Fatalf("RemoteNames: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"origin"}) {
		t.Errorf("RemoteNames() = %v, want [origin]", names)
	}

	empty := t.TempDir()
	r.run(empty, "init", "-q")
	t.Chdir(empty)

	names, err = RemoteNames()
	if err != nil {
		t.Fatalf("RemoteNames in a repo with no remotes: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("RemoteNames() = %v, want none", names)
	}
}

func TestCurrentBranchNamesTheBranchAndRejectsDetachedHead(t *testing.T) {
	r := newTestRepo(t)
	sha := r.Commit("a.txt", "one\n", "initial")
	t.Chdir(r.Work)

	branch, err := CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("CurrentBranch() = %q, want main", branch)
	}

	r.run(r.Work, "checkout", "-q", sha)
	if _, err := CurrentBranch(); err == nil || !strings.Contains(err.Error(), "not on a branch") {
		t.Errorf("CurrentBranch() on a detached HEAD = %v, want an error saying HEAD is not on a branch", err)
	}
}

func TestInstallHookReportsAnUnwritableHookPath(t *testing.T) {
	r := newTestRepo(t)
	if err := os.MkdirAll(filepath.Join(r.Bare, "hooks", "pre-receive"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := InstallHook(r.Bare, "http://127.0.0.1:10001", "proj", "main"); err == nil {
		t.Error("InstallHook with a directory where the hook belongs = nil, want error")
	}
}

func TestBranchExistsGetSHAGetMergeBase(t *testing.T) {
	r := newTestRepo(t)
	base := r.Commit("a.txt", "one\n", "initial")
	r.Branch("feature")
	head := r.Commit("b.txt", "two\n", "on feature")

	if !BranchExists(r.Work, "main") {
		t.Error("expected main to exist")
	}
	if !BranchExists(r.Work, "feature") {
		t.Error("expected feature to exist")
	}
	if BranchExists(r.Work, "nope") {
		t.Error("expected nope not to exist")
	}

	gotHEAD, err := GetSHA(r.Work, "HEAD")
	if err != nil {
		t.Fatalf("GetSHA HEAD: %v", err)
	}
	if gotHEAD != head {
		t.Errorf("GetSHA HEAD = %s, want %s", gotHEAD, head)
	}
	if _, err := GetSHA(r.Work, "not-a-ref"); err == nil {
		t.Error("expected error for bogus ref")
	}

	gotBase, err := GetMergeBase(r.Work, "main", head)
	if err != nil {
		t.Fatalf("GetMergeBase: %v", err)
	}
	if gotBase != base {
		t.Errorf("GetMergeBase = %s, want %s", gotBase, base)
	}
}

func TestIsMerged(t *testing.T) {
	r := newTestRepo(t)
	r.Commit("a.txt", "one\n", "initial")
	r.Branch("feature")
	head := r.Commit("b.txt", "two\n", "on feature")
	r.Checkout("main")

	if IsMerged(r.Work, "", "main") {
		t.Error("empty head should not be merged")
	}
	if IsMerged(r.Work, head, "no-such-branch") {
		t.Error("missing base branch should not be merged")
	}
	if IsMerged(r.Work, head, "main") {
		t.Error("unmerged branch should not be merged")
	}

	r.MergeNoFF("feature")
	if !IsMerged(r.Work, head, "main") {
		t.Error("expected branch to be merged after --no-ff merge")
	}
}

func TestIsZeroSHA(t *testing.T) {
	cases := []struct {
		name string
		sha  string
		want bool
	}{
		{"sha1 zeros", strings.Repeat("0", 40), true},
		{"sha256 zeros", strings.Repeat("0", 64), true},
		{"empty", "", false},
		{"mixed", strings.Repeat("0", 39) + "1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsZeroSHA(c.sha); got != c.want {
				t.Errorf("IsZeroSHA(%q) = %v, want %v", c.sha, got, c.want)
			}
		})
	}
}

func TestCreateBareRepoFailsWhenParentIsFile(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	path := filepath.Join(blocker, "repo.git")

	if err := CreateBareRepo(path); err == nil {
		t.Fatal("CreateBareRepo: want error when a parent path component is a file")
	}
}

func TestCommitHistoryInvalidRangeErrors(t *testing.T) {
	r := newTestRepo(t)
	r.Commit("a.txt", "one\n", "initial")

	t.Run("CommitBoundaries", func(t *testing.T) {
		if _, err := CommitBoundaries(r.Work, "bogus1", "bogus2"); err == nil {
			t.Fatal("want error for invalid range")
		}
	})
	t.Run("GetCommitLog", func(t *testing.T) {
		if _, err := GetCommitLog(r.Work, "bogus1", "bogus2"); err == nil {
			t.Fatal("want error for invalid range")
		}
	})
}

func TestDiffErrorsIncludeGitStderrForUnknownSHA(t *testing.T) {
	r := newTestRepo(t)
	r.Commit("a.txt", "one\n", "initial")

	const from = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	const to = "beefdeadbeefdeadbeefdeadbeefdeadbeefdead"

	assertGitStderr := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected error for unknown SHAs")
		}
		if !strings.HasPrefix(err.Error(), "git diff: ") {
			t.Fatalf("error = %q, want prefix %q", err.Error(), "git diff: ")
		}
		if strings.TrimPrefix(err.Error(), "git diff: ") == "" {
			t.Fatal("expected git's stderr in the error")
		}
	}

	t.Run("GetPathDiff", func(t *testing.T) {
		_, err := GetPathDiff(r.Work, from, to, "a.txt")
		assertGitStderr(t, err)
	})
	t.Run("getDiff", func(t *testing.T) {
		_, err := getDiff(r.Work, from, to)
		assertGitStderr(t, err)
	})
}

func TestGetCommitLogOldestFirstWithBodyAndTime(t *testing.T) {
	r := newTestRepo(t)
	sha1 := r.Commit("a.txt", "one\n", "first commit")
	sha2 := r.Commit("b.txt", "two\n", "second commit\n\nbody line one\nbody line two")
	sha3 := r.Commit("c.txt", "three\n", "third commit")

	commits, err := GetCommitLog(r.Work, EmptyTreeSHA, sha3)
	if err != nil {
		t.Fatalf("GetCommitLog: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("got %d commits, want 3", len(commits))
	}
	wantSHAs := []string{sha1, sha2, sha3}
	for i, c := range commits {
		if c.SHA != wantSHAs[i] {
			t.Errorf("commit %d SHA = %s, want %s", i, c.SHA, wantSHAs[i])
		}
		wantTime := r.Git("show", "-s", "--format=%at", c.SHA)
		if strconv.FormatInt(c.Time, 10) != wantTime {
			t.Errorf("commit %d Time = %d, want %s", i, c.Time, wantTime)
		}
	}
	if commits[0].Message != "first commit" {
		t.Errorf("commits[0].Message = %q", commits[0].Message)
	}
	if commits[1].Message != "second commit" {
		t.Errorf("commits[1].Message = %q", commits[1].Message)
	}
	if commits[1].Body != "body line one\nbody line two" {
		t.Errorf("commits[1].Body = %q", commits[1].Body)
	}
}

func TestCommitBoundaries(t *testing.T) {
	r := newTestRepo(t)
	base := r.Commit("a.txt", "one\n", "initial")
	c1 := r.Commit("b.txt", "two\n", "c1")
	c2 := r.Commit("c.txt", "three\n", "c2")
	c3 := r.Commit("d.txt", "four\n", "c3")

	got, err := CommitBoundaries(r.Work, base, c3)
	if err != nil {
		t.Fatalf("CommitBoundaries: %v", err)
	}
	want := []string{base, c1, c2, c3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("boundaries = %v, want %v", got, want)
	}
}

func TestGetPathDiffScopedToPath(t *testing.T) {
	r := newTestRepo(t)
	from := r.Commit("a.txt", "line1\nline2\nline3\n", "initial")
	r.Write("a.txt", "line1\nCHANGED\nline3\n")
	r.Write("b.txt", "other file change\n")
	r.Git("add", "-A")
	r.Git("commit", "-q", "-m", "modify both files")
	to := r.SHA("HEAD")

	diff, err := GetPathDiff(r.Work, from, to, "a.txt")
	if err != nil {
		t.Fatalf("GetPathDiff: %v", err)
	}
	if !strings.Contains(diff, "a.txt") {
		t.Fatalf("diff missing a.txt header: %s", diff)
	}
	if strings.Contains(diff, "b.txt") {
		t.Fatalf("diff should be scoped to a.txt, got: %s", diff)
	}
	if !strings.Contains(diff, "-line2") || !strings.Contains(diff, "+CHANGED") {
		t.Fatalf("diff missing changed line: %s", diff)
	}
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, " ") {
			t.Fatalf("expected -U0 (no context lines), got context line %q in: %s", line, diff)
		}
	}
}

func TestBlobExists(t *testing.T) {
	r := newTestRepo(t)
	sha := r.Commit("a.txt", "content\n", "add a")
	after := r.Remove("a.txt", "remove a")

	if !BlobExists(r.Work, sha, "a.txt") {
		t.Error("expected a.txt to exist at its own commit")
	}
	if BlobExists(r.Work, after, "a.txt") {
		t.Error("expected a.txt to be gone after removal")
	}
}

func TestGetBranchHeadsIncludesSlashedBranchesAndEmptyRepo(t *testing.T) {
	r := newTestRepo(t)

	heads, err := GetBranchHeads(r.Bare)
	if err != nil {
		t.Fatalf("GetBranchHeads on empty repo: %v", err)
	}
	if len(heads) != 0 {
		t.Fatalf("expected no heads in empty bare repo, got %v", heads)
	}

	mainSHA := r.Commit("a.txt", "one\n", "initial")
	r.Push("main")
	r.Branch("feature/thing")
	featSHA := r.Commit("b.txt", "two\n", "on feature")
	r.Push("feature/thing")

	heads, err = GetBranchHeads(r.Bare)
	if err != nil {
		t.Fatalf("GetBranchHeads: %v", err)
	}
	if heads["main"] != mainSHA {
		t.Errorf("heads[main] = %s, want %s", heads["main"], mainSHA)
	}
	if heads["feature/thing"] != featSHA {
		t.Errorf("heads[feature/thing] = %s, want %s", heads["feature/thing"], featSHA)
	}
}

func TestWindowLines(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	cases := []struct {
		name      string
		startLine int
		count     int
		want      []string
	}{
		{"start below 1 clamps to 1", 0, 2, []string{"a", "b"}},
		{"start past EOF returns empty", 10, 2, []string{}},
		{"count clamped at EOF", 4, 10, []string{"d", "e"}},
		{"count 0 means to EOF", 3, 0, []string{"c", "d", "e"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := WindowLines(lines, c.startLine, c.count)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("WindowLines(%d, %d) = %v, want %v", c.startLine, c.count, got, c.want)
			}
		})
	}
}
