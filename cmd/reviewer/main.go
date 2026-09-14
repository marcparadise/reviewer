package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"marcparadise.io/projects/reviewer/internal"
)

type app struct {
	v            *viper.Viper
	dataDir      string
	pollInterval time.Duration
}

func newRootCmd() *cobra.Command {
	a := &app{v: viper.New(), pollInterval: 5 * time.Second}
	return a.rootCmd()
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "reviewer",
		Short:        "Local git-based code review tool",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return internal.InitViper(a.v, a.resolvedDataDir())
		},
	}

	root.PersistentFlags().StringVar(&a.dataDir, "data-dir", "", "data directory (default: ~/.config/reviewer)")
	root.PersistentFlags().Int("port", 0, "server port (default 8091")
	a.v.BindPFlag("port", root.PersistentFlags().Lookup("port"))

	root.AddCommand(
		a.serveCmd(),
		a.initCmd(),
		a.listCmd(),
		a.statusCmd(),
		a.waitCmd(),
		a.responseBlockCmd(),
		a.installHooksCmd(),
	)
	return root
}

func (a *app) resolvedDataDir() string {
	if a.dataDir != "" {
		return a.dataDir
	}
	return internal.DefaultDataDir()
}

func (a *app) makeConfig() (*internal.Config, error) {
	cfg := internal.NewFromViper(a.v, a.resolvedDataDir())
	if err := cfg.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("creating data dirs: %w", err)
	}
	return cfg, nil
}

func openDB(cfg *internal.Config) (*internal.DB, error) {
	database, err := internal.Open(cfg.DBPath())
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	return database, nil
}

// --- serve ---

func (a *app) serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the reviewer server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.makeConfig()
			if err != nil {
				return err
			}
			database, err := openDB(cfg)
			if err != nil {
				return err
			}
			defer database.Close()

			srv := internal.New(cfg, database)
			interval, _ := cmd.Flags().GetDuration("resync-interval")
			srv.StartResync(interval)
			return srv.ListenAndServe()
		},
	}
	cmd.Flags().Duration("resync-interval", 5*time.Minute,
		"how often to reconcile reviews against the repo refs (0 disables)")
	return cmd
}

// --- init ---

func (a *app) initCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Register this repository as a project and wire its review remote",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.makeConfig()
			if err != nil {
				return err
			}
			database, err := openDB(cfg)
			if err != nil {
				return err
			}
			defer database.Close()

			if !internal.InGitRepo() {
				return errors.New("not in a git repository: run init from the project's working tree")
			}
			top, err := internal.RepoTopLevel()
			if err != nil {
				return fmt.Errorf("reading this repository's location: %w", err)
			}
			slug := slugify(filepath.Base(top))
			if slug == "" {
				return fmt.Errorf("cannot name a project after the directory %q", filepath.Base(top))
			}

			baseBranch := a.v.GetString("base-branch")
			out := cmd.OutOrStdout()

			project, _, err := matchingProject(database)
			if err != nil {
				return err
			}

			switch {
			case project == nil:
				if taken, err := database.GetProject(slug); err != nil {
					return err
				} else if taken != nil {
					return fmt.Errorf("project %q is already registered to %s\n  rename this directory, or remove that project", slug, taken.RepoPath)
				}
				repoPath := filepath.Join(cfg.ReposDir(), slug+".git")
				if err := internal.CreateBareRepo(repoPath); err != nil {
					return fmt.Errorf("creating bare repo: %w", err)
				}
				if err := internal.InstallHook(repoPath, cfg.ReviewerURL(), slug, baseBranch); err != nil {
					return fmt.Errorf("installing hook: %w", err)
				}
				if project, err = database.CreateProject(slug, repoPath, baseBranch); err != nil {
					return fmt.Errorf("creating project: %w", err)
				}
				fmt.Fprintf(out, "Registered project %q\n", slug)
			case project.Slug != slug:
				taken, err := database.GetProject(slug)
				if err != nil {
					return err
				}
				if taken != nil {
					return fmt.Errorf("project %q is already registered to %s\n  this repository is registered as %q", slug, taken.RepoPath, project.Slug)
				}
				if err := database.UpdateProjectSlug(project.ID, slug); err != nil {
					return fmt.Errorf("renaming project: %w", err)
				}
				if err := internal.InstallHook(project.RepoPath, cfg.ReviewerURL(), slug, project.BaseBranch); err != nil {
					return fmt.Errorf("installing hook: %w", err)
				}
				fmt.Fprintf(out, "Renamed project %q to %q\n", project.Slug, slug)
				project.Slug = slug
			default:
				fmt.Fprintf(out, "Project %q is already registered\n", slug)
			}

			if err := ensureReviewRemote(project.RepoPath); err != nil {
				return err
			}
			fmt.Fprintf(out, "Remote 'review' -> %s\n", project.RepoPath)

			if !internal.BranchExists(top, baseBranch) {
				fmt.Fprintf(out, "\nNo local branch %q to seed the reviewer repo with.\n", baseBranch)
			} else if err := internal.Push("review", baseBranch); err != nil {
				fmt.Fprintf(out, "\nCould not push %q to the reviewer server (%v).\nOnce it is reachable: git push review %s\n", baseBranch, err, baseBranch)
			} else {
				fmt.Fprintf(out, "Seeded the base branch %q\n", baseBranch)
			}

			fmt.Fprintf(out, "\nPush feature branches for review:\n  git push review <feature-branch>\n")
			fmt.Fprintf(out, "Review at %s/#/projects/%s\n", cfg.ReviewerURL(), project.Slug)
			return nil
		},
	}
	cmd.Flags().String("base-branch", "main", "base branch to diff against")
	a.v.BindPFlag("base-branch", cmd.Flags().Lookup("base-branch"))
	return cmd
}

func ensureReviewRemote(repoPath string) error {
	url, err := internal.GetRemoteURL(reviewRemote)
	if err != nil {
		return internal.AddRemote(reviewRemote, repoPath)
	}
	if canonicalPath(url) != canonicalPath(repoPath) {
		return fmt.Errorf("remote %q already points at %s\n  expected %s\n  rename or remove it, then run init again", reviewRemote, url, repoPath)
	}
	return nil
}

// --- list ---

func (a *app) listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List projects and open review counts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.makeConfig()
			if err != nil {
				return err
			}
			database, err := openDB(cfg)
			if err != nil {
				return err
			}
			defer database.Close()

			projects, err := database.ListProjects()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(projects) == 0 {
				fmt.Fprintln(out, "No projects. Run: reviewer init")
				return nil
			}
			for _, p := range projects {
				open, err := database.CountReviews(p.ID, "open")
				if err != nil {
					return fmt.Errorf("could not count open reviews for %q: %w", p.Slug, err)
				}
				fmt.Fprintf(out, "%-20s  (%d open)\n", p.Slug, open)
				fmt.Fprintf(out, "  repo: %s\n", p.RepoPath)
			}
			return nil
		},
	}
}

// --- status ---

func (a *app) statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [branch]",
		Short: "Print review status for a branch",
		Args:  branchArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")

			cfg, err := a.makeConfig()
			if err != nil {
				return err
			}
			database, err := openDB(cfg)
			if err != nil {
				return err
			}
			defer database.Close()

			p, err := resolveProject(database)
			if err != nil {
				return err
			}
			branch, err := resolveBranch(firstArg(args))
			if err != nil {
				return err
			}
			rev, err := database.GetReviewByBranch(p.ID, branch)
			if err != nil || rev == nil {
				return fmt.Errorf("no review for branch %q", branch)
			}
			comments, err := database.ListCommentsSince(rev.ID, rev.LastOpenedAt)
			if err != nil {
				return fmt.Errorf("could not read comments for %q: %w", branch, err)
			}
			printReviewStatus(cmd.OutOrStdout(), p, rev, comments, asJSON)
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "output as JSON")
	return cmd
}

func printReviewStatus(w io.Writer, p *internal.Project, rev *internal.Review, comments []*internal.Comment, asJSON bool) {
	if asJSON {
		type output struct {
			*internal.Review
			Comments []*internal.Comment `json:"comments"`
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(output{Review: rev, Comments: comments})
		return
	}

	fmt.Fprintf(w, "Project: %s\n", p.Slug)
	fmt.Fprintf(w, "Branch:  %s\n", rev.Branch)
	fmt.Fprintf(w, "Status:  %s\n", rev.Status)
	fmt.Fprintf(w, "Updated: %s\n", rev.UpdatedAt)
	if len(comments) > 0 {
		fmt.Fprintf(w, "\nComments (%d):\n", len(comments))
		for _, c := range comments {
			loc := commentLocation(c)
			fmt.Fprintf(w, "\n  %s\n", loc)
			for line := range strings.SplitSeq(c.Body, "\n") {
				fmt.Fprintf(w, "    %s\n", line)
			}
		}
	}
}

// --- wait ---

func (a *app) waitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait [branch]",
		Short: "Block until a review is no longer open, then print its status",
		Args:  branchArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			timeout, _ := cmd.Flags().GetDuration("timeout")

			cfg, err := a.makeConfig()
			if err != nil {
				return err
			}
			database, err := openDB(cfg)
			if err != nil {
				return err
			}
			defer database.Close()

			p, err := resolveProject(database)
			if err != nil {
				return err
			}
			branch, err := resolveBranch(firstArg(args))
			if err != nil {
				return err
			}

			start := time.Now()
			for {
				rev, err := database.GetReviewByBranch(p.ID, branch)
				if err != nil || rev == nil {
					return fmt.Errorf("no review for branch %q", branch)
				}
				if rev.Status != "open" {
					comments, err := database.ListCommentsSince(rev.ID, rev.LastOpenedAt)
					if err != nil {
						return fmt.Errorf("could not read comments for %q: %w", branch, err)
					}
					printReviewStatus(cmd.OutOrStdout(), p, rev, comments, asJSON)
					return nil
				}
				if timeout > 0 && time.Since(start) >= timeout {
					return fmt.Errorf("timed out waiting for review of %q", branch)
				}
				time.Sleep(a.pollInterval)
			}
		},
	}
	cmd.Flags().Duration("timeout", 0, "maximum time to wait (default: no timeout)")
	cmd.Flags().Bool("json", false, "output as JSON")
	return cmd
}

// --- response-block ---

func (a *app) responseBlockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "response-block [branch]",
		Short: "Print a commit block of outstanding review comments, or nothing",
		Args:  branchArg,
		RunE: func(cmd *cobra.Command, args []string) error {

			cfg, err := a.makeConfig()
			if err != nil {
				return err
			}
			database, err := openDB(cfg)
			if err != nil {
				return err
			}
			defer database.Close()

			p, err := resolveProject(database)
			if err != nil {
				if errors.Is(err, errProjectNotResolved) {
					return nil
				}
				return err
			}
			branch, err := resolveBranch(firstArg(args))
			if err != nil {
				return nil
			}
			rev, err := database.GetReviewByBranch(p.ID, branch)
			if err != nil {
				return err
			}
			if rev == nil {
				return nil
			}
			if rev.Status != "changes_requested" {
				return nil
			}
			comments, err := database.ListCommentsSince(rev.ID, rev.LastOpenedAt)
			if err != nil {
				return fmt.Errorf("could not read comments for %q: %w", branch, err)
			}
			if len(comments) == 0 {
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Review-Response: %s %s\n", p.Slug, branch)
			fmt.Fprintf(out, "In response to the following review comments:\n")
			for _, c := range comments {
				loc := commentLocation(c)
				fmt.Fprintf(out, "\n  %s\n", loc)
				for line := range strings.SplitSeq(c.Body, "\n") {
					fmt.Fprintf(out, "    %s\n", line)
				}
			}
			return nil
		},
	}
	return cmd
}

// --- install-hooks ---

func (a *app) installHooksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-hooks",
		Short: "Reinstall git hooks for all projects (e.g. after upgrading)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.makeConfig()
			if err != nil {
				return err
			}
			database, err := openDB(cfg)
			if err != nil {
				return err
			}
			defer database.Close()

			projects, err := database.ListProjects()
			if err != nil {
				return err
			}
			for _, p := range projects {
				if err := internal.InstallHook(p.RepoPath, cfg.ReviewerURL(), p.Slug, p.BaseBranch); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "error installing hook for %s: %v\n", p.Slug, err)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "installed hook for %s\n", p.Slug)
				}
			}
			return nil
		},
	}
}

// --- resolving the current repo ---

const reviewRemote = "review"

func branchArg(_ *cobra.Command, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("accepts at most 1 argument (the branch), received %d", len(args))
	}
	return nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

var errProjectNotResolved = errors.New("no reviewer project for this repository")

func resolveProject(database *internal.DB) (*internal.Project, error) {
	if !internal.InGitRepo() {
		return nil, fmt.Errorf("%w: not inside a git repository", errProjectNotResolved)
	}

	p, remotes, err := matchingProject(database)
	if err != nil {
		return nil, err
	}
	if p != nil {
		return p, nil
	}

	if len(remotes) == 0 {
		return nil, fmt.Errorf("%w: it has no git remotes\n  run 'reviewer init' here to register it", errProjectNotResolved)
	}
	checked := make([]string, 0, len(remotes))
	for _, r := range remotes {
		checked = append(checked, fmt.Sprintf("%s -> %s", r.name, r.url))
	}
	return nil, fmt.Errorf("%w\n  checked: %s\n  run 'reviewer init' here to register it",
		errProjectNotResolved, strings.Join(checked, "\n           "))
}

func matchingProject(database *internal.DB) (*internal.Project, []remoteURL, error) {
	remotes, err := repoRemotes()
	if err != nil {
		return nil, nil, err
	}
	if len(remotes) == 0 {
		return nil, nil, nil
	}

	projects, err := database.ListProjects()
	if err != nil {
		return nil, nil, err
	}

	var matches []*internal.Project
	for _, r := range remotes {
		if p := projectAtPath(projects, r.url); p != nil && !slices.Contains(matches, p) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return nil, remotes, nil
	case 1:
		return matches[0], remotes, nil
	}

	slugs := make([]string, 0, len(matches))
	for _, p := range matches {
		slugs = append(slugs, p.Slug)
	}
	return nil, remotes, fmt.Errorf("this repo's remotes point at %d registered projects (%s)\n  remove or rename all but one, then run init again",
		len(matches), strings.Join(slugs, ", "))
}

type remoteURL struct {
	name string
	url  string
}

func repoRemotes() ([]remoteURL, error) {
	names, err := internal.RemoteNames()
	if err != nil {
		return nil, fmt.Errorf("could not list this repo's git remotes: %w", err)
	}

	out := make([]remoteURL, 0, len(names))
	for _, name := range names {
		url, err := internal.GetRemoteURL(name)
		if err != nil {
			continue
		}
		out = append(out, remoteURL{name: name, url: url})
	}
	return out, nil
}

func projectAtPath(projects []*internal.Project, remoteURL string) *internal.Project {
	remotePath := canonicalPath(remoteURL)
	if remotePath == "" {
		return nil
	}
	for _, p := range projects {
		if canonicalPath(p.RepoPath) == remotePath {
			return p
		}
	}
	return nil
}

func canonicalPath(path string) string {
	if path == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(path, "file://"); ok && strings.HasPrefix(rest, "/") {
		path = rest
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func resolveBranch(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	branch, err := internal.CurrentBranch()
	if err != nil {
		return "", fmt.Errorf("could not determine the current branch, name it explicitly: %w", err)
	}
	return branch, nil
}

func commentLocation(c *internal.Comment) string {
	if c.FilePath == nil {
		return "(general)"
	}
	loc := *c.FilePath
	if c.LineNumber != nil {
		loc += fmt.Sprintf(":%d", *c.LineNumber)
		if c.LineEnd != nil {
			loc += fmt.Sprintf("-%d", *c.LineEnd)
		}
	}
	return loc
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlnum.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
