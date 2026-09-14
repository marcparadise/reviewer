package internal

import (
	"fmt"
	"net"
	"net/http/httptest"
	"os/exec"
	"testing"
)

func TestHooksIntegration(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not on PATH")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}

	repo := newTestRepo(t)
	db := openTestDB(t)

	ts := httptest.NewUnstartedServer(nil)
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	srv := New(&Config{Port: port, DataDir: t.TempDir()}, db)
	ts.Config.Handler = srv.Handler()
	ts.Start()

	project, err := db.CreateProject("proj", repo.Bare, "main")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	reviewerURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := InstallHook(repo.Bare, reviewerURL, project.Slug, "main"); err != nil {
		t.Fatalf("InstallHook: %v", err)
	}

	mainSHA := repo.Commit("README.md", "hello\n", "initial commit")
	repo.Push("main")

	t.Run("pushing the base branch creates no review", func(t *testing.T) {
		rev, err := db.GetReviewByBranch(project.ID, "main")
		if err != nil {
			t.Fatalf("GetReviewByBranch: %v", err)
		}
		if rev != nil {
			t.Fatalf("expected no review for base branch, got %+v", rev)
		}
	})

	t.Run("feature branch push creates an open review with head and merge base", func(t *testing.T) {
		repo.Branch("feature-x")
		featureHead := repo.Commit("feature.txt", "feature work\n", "add feature")
		repo.Push("feature-x")

		rev, err := db.GetReviewByBranch(project.ID, "feature-x")
		if err != nil {
			t.Fatalf("GetReviewByBranch: %v", err)
		}
		if rev == nil {
			t.Fatal("expected a review to be created")
		}
		if rev.Status != "open" {
			t.Errorf("Status = %q, want open", rev.Status)
		}
		if rev.HeadSHA != featureHead {
			t.Errorf("HeadSHA = %s, want %s", rev.HeadSHA, featureHead)
		}
		if rev.BaseSHA != mainSHA {
			t.Errorf("BaseSHA = %s, want %s", rev.BaseSHA, mainSHA)
		}
	})

	t.Run(`branch name with a double quote round-trips intact`, func(t *testing.T) {
		repo.Checkout("main")
		repo.Branch(`feat"ure`)
		repo.Commit("quote.txt", "content\n", "add quoted branch file")
		repo.Push(`feat"ure`)

		rev, err := db.GetReviewByBranch(project.ID, `feat"ure`)
		if err != nil {
			t.Fatalf("GetReviewByBranch: %v", err)
		}
		if rev == nil {
			t.Fatal("expected a review to be created for the quoted branch")
		}
		if rev.Branch != `feat"ure` {
			t.Errorf("Branch = %q, want %q", rev.Branch, `feat"ure`)
		}
	})

	t.Run("pushing a branch deletion closes the review", func(t *testing.T) {
		cmd := exec.Command("git", "push", "origin", ":refs/heads/feature-x")
		cmd.Dir = repo.Work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("delete push: %v\n%s", err, out)
		}

		rev, err := db.GetReviewByBranch(project.ID, "feature-x")
		if err != nil {
			t.Fatalf("GetReviewByBranch: %v", err)
		}
		if rev == nil {
			t.Fatal("expected the review to still exist, closed")
		}
		if rev.Status != "closed" {
			t.Errorf("Status = %q, want closed", rev.Status)
		}
	})

	t.Run("server closed rejects the push via pre-receive", func(t *testing.T) {
		ts.Close()

		repo.Checkout("main")
		repo.Branch("feature-y")
		repo.Commit("late.txt", "too late\n", "add feature y")

		cmd := exec.Command("git", "push", "origin", "feature-y")
		cmd.Dir = repo.Work
		if err := cmd.Run(); err == nil {
			t.Fatal("expected push to fail once the server is down")
		}
	})
}
