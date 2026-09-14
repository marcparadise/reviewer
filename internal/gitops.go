package internal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hookTemplate is the pre-receive hook installed into each bare repo. It only
// checks reachability: a review written here would be committed to a push git
// has not yet accepted, and the incoming objects sit in a quarantine the server
// cannot read. Rejecting the push keeps a landed push from having no review.
const hookTemplate = `#!/bin/sh
# Reviewer pre-receive hook — managed by reviewer, do not edit by hand
REVIEWER_URL="%s"
PROJECT_SLUG="%s"

if ! curl -sf "$REVIEWER_URL/api/projects/$PROJECT_SLUG" >/dev/null; then
    echo "reviewer: cannot reach project '$PROJECT_SLUG' at $REVIEWER_URL" >&2
    echo "reviewer: push rejected — is the server running?" >&2
    exit 1
fi
`

const postReceiveTemplate = `#!/bin/sh
# Reviewer post-receive hook — managed by reviewer, do not edit by hand
REVIEWER_URL="%s"
PROJECT_SLUG="%s"
BASE_BRANCH="%s"

while read -r old_sha new_sha ref; do
    case "$ref" in
        refs/heads/*)
            branch="${ref#refs/heads/}"
            # Escape backslash and double-quote so a branch name containing them
            # can't break out of the JSON string. Git refnames forbid control
            # chars and backslashes, so these two are the only JSON-significant
            # characters that can legitimately appear.
            esc_branch=$(printf '%%s' "$branch" | sed 's/\\/\\\\/g; s/"/\\"/g')
            payload=$(printf '{"project_slug":"%%s","branch":"%%s","new_sha":"%%s"}' \
                "$PROJECT_SLUG" "$esc_branch" "$new_sha")
            if ! curl -sf -X POST "$REVIEWER_URL/api/hooks/post-receive" \
                -H 'Content-Type: application/json' \
                -d "$payload" >/dev/null; then
                echo "reviewer: could not record '$branch' at $REVIEWER_URL" >&2
            fi
            case "$new_sha" in *[!0]*) is_delete=no ;; *) is_delete=yes ;; esac
            if [ "$branch" != "$BASE_BRANCH" ] && [ "$is_delete" = no ]; then
                echo "reviewer: review ready at $REVIEWER_URL/#/projects/$PROJECT_SLUG/$branch" >&2
            fi
            ;;
    esac
done
`

type Commit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Body    string `json:"body"`
	Time    int64  `json:"time"` // Unix timestamp of author date
}

func gitRaw(repoPath string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return out, fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

func git(repoPath string, args ...string) (string, error) {
	out, err := gitRaw(repoPath, args...)
	return strings.TrimSpace(string(out)), err
}

func CreateBareRepo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := git("", "init", "--bare", path)
	return err
}

func InstallHook(repoPath, reviewerURL, projectSlug, baseBranch string) error {
	pre := fmt.Sprintf(hookTemplate, reviewerURL, projectSlug)
	if err := os.WriteFile(filepath.Join(repoPath, "hooks", "pre-receive"), []byte(pre), 0o755); err != nil {
		return err
	}
	post := fmt.Sprintf(postReceiveTemplate, reviewerURL, projectSlug, baseBranch)
	return os.WriteFile(filepath.Join(repoPath, "hooks", "post-receive"), []byte(post), 0o755)
}

func GetRemoteURL(remote string) (string, error) {
	return git("", "remote", "get-url", remote)
}

func InGitRepo() bool {
	_, err := git("", "rev-parse", "--git-dir")
	return err == nil
}

func RepoTopLevel() (string, error) {
	return git("", "rev-parse", "--show-toplevel")
}

func AddRemote(remote, url string) error {
	_, err := git("", "remote", "add", remote, url)
	return err
}

func Push(remote, ref string) error {
	_, err := git("", "push", remote, ref)
	return err
}

func RemoteNames() ([]string, error) {
	out, err := git("", "remote")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Fields(out), nil
}

func CurrentBranch() (string, error) {
	branch, err := git("", "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return "", errors.New("HEAD is not on a branch")
	}
	return branch, nil
}

func BranchExists(repoPath, branch string) bool {
	_, err := git(repoPath, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

func GetSHA(repoPath, ref string) (string, error) {
	return git(repoPath, "rev-parse", ref)
}

// EmptyTreeSHA is the SHA of git's empty tree object, used as a base when the
// base branch doesn't exist (e.g. first-ever push to a new repo).
const EmptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// IsZeroSHA reports whether s is git's all-zero "null" object id, which git
// sends as the new SHA when a ref is being deleted. Works for both SHA-1 (40
// zeros) and SHA-256 (64 zeros) repositories.
func IsZeroSHA(s string) bool {
	return s != "" && strings.Trim(s, "0") == ""
}

func GetMergeBase(repoPath, baseBranch, headSHA string) (string, error) {
	return git(repoPath, "merge-base", "refs/heads/"+baseBranch, headSHA)
}

// IsMerged reports whether headSHA is contained in (an ancestor of) the base
// branch — i.e. the review's commits have landed on it. With the --no-ff merges
// this tool uses, a merged branch's head becomes an ancestor of the base branch.
// Returns false if the base branch doesn't exist or git errors.
func IsMerged(repoPath, headSHA, baseBranch string) bool {
	if headSHA == "" || !BranchExists(repoPath, baseBranch) {
		return false
	}
	_, err := git(repoPath, "merge-base", "--is-ancestor", headSHA, "refs/heads/"+baseBranch)
	return err == nil
}

func getDiff(repoPath, baseSHA, headSHA string) (string, error) {
	out, err := gitRaw(repoPath, "diff", baseSHA+".."+headSHA)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func GetCommitLog(repoPath, baseSHA, headSHA string) ([]Commit, error) {
	// %H = full SHA, %s = subject, %b = body, %at = author Unix timestamp
	// \x1f separates fields, \x1e separates records; --reverse gives oldest-first order
	format := "--format=%H\x1f%s\x1f%b\x1f%at\x1e"
	args := []string{"log", "--reverse", format, baseSHA + ".." + headSHA}
	if baseSHA == EmptyTreeSHA {
		args = []string{"log", "--reverse", format, headSHA}
	}
	out, err := git(repoPath, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, record := range strings.Split(out, "\x1e") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\x1f", 4)
		if len(parts) < 2 {
			continue
		}
		c := Commit{SHA: strings.TrimSpace(parts[0]), Message: strings.TrimSpace(parts[1])}
		if len(parts) >= 3 {
			c.Body = strings.TrimSpace(parts[2])
		}
		if len(parts) == 4 {
			fmt.Sscan(strings.TrimSpace(parts[3]), &c.Time)
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// CommitBoundaries returns [baseSHA, sha0, sha1, ...] oldest-first, giving every
// SHA that may legally serve as a range boundary between baseSHA and headSHA.
// The slice is ordered so index 0 is the exclusive lower bound (baseSHA itself)
// and index N is the most recent commit. Callers can translate a two-click range
// [i, j] (i < j) to a git diff of boundaries[i]..boundaries[j].
func CommitBoundaries(repoPath, baseSHA, headSHA string) ([]string, error) {
	commits, err := GetCommitLog(repoPath, baseSHA, headSHA)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(commits)+1)
	out = append(out, baseSHA)
	for _, c := range commits {
		out = append(out, c.SHA)
	}
	return out, nil
}

// GetFileContent returns every line of the file at sha:filePath, 1-indexed by
// slice position+1, with the trailing empty line from a final newline removed.
func GetPathDiff(repoPath, fromSHA, toSHA, path string) (string, error) {
	out, err := gitRaw(repoPath, "diff", "-U0", fromSHA+".."+toSHA, "--", path)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func BlobExists(repoPath, sha, path string) bool {
	_, err := git(repoPath, "cat-file", "-e", sha+":"+path)
	return err == nil
}

func GetBranchHeads(repoPath string) (map[string]string, error) {
	out, err := git(repoPath, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")
	if err != nil {
		return nil, err
	}
	heads := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, sha, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		heads[name] = sha
	}
	return heads, nil
}

func GetFileContent(repoPath, sha, filePath string) ([]string, error) {
	out, err := gitRaw(repoPath, "show", sha+":"+filePath)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"), nil
}

// WindowLines returns lines [startLine, startLine+count) from all. startLine is
// 1-based. If count <= 0, returns all lines from startLine to EOF.
func WindowLines(all []string, startLine, count int) []string {
	total := len(all)
	if startLine < 1 {
		startLine = 1
	}
	start := startLine - 1
	if start >= total {
		return []string{}
	}
	end := total
	if count > 0 {
		end = start + count
		if end > total {
			end = total
		}
	}
	return all[start:end]
}
