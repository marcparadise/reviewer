package internal

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
)

// highlightCSS serves the generated syntax-highlighting stylesheet (light theme
// by default, dark rules scoped under html.dark). It is stable for the life of
// the process but changes across builds, so it is sent with no-cache to force
// revalidation — otherwise a browser keeps a stale sheet after a server upgrade.
func (s *Server) highlightCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	io.WriteString(w, HighlightCSS())
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, data any, status ...int) {
	code := http.StatusOK
	if len(status) > 0 {
		code = status[0]
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func writeErr(w http.ResponseWriter, msg string, status int) {
	writeJSON(w, map[string]string{"error": msg}, status)
}

func readJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

func (s *Server) project(w http.ResponseWriter, slug string) *Project {
	p, err := s.db.GetProject(slug)
	if err != nil {
		writeErr(w, "database error", http.StatusInternalServerError)
		return nil
	}
	if p == nil {
		writeErr(w, "project not found", http.StatusNotFound)
		return nil
	}
	return p
}

func (s *Server) review(w http.ResponseWriter, idStr string, p *Project) *Review {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, "invalid review id", http.StatusBadRequest)
		return nil
	}
	r, err := s.db.GetReview(id)
	if err != nil {
		writeErr(w, "database error", http.StatusInternalServerError)
		return nil
	}
	if r == nil || r.ProjectID != p.ID {
		writeErr(w, "review not found", http.StatusNotFound)
		return nil
	}
	return r
}

// diffRange resolves the (base, head) SHA pair for a diff request.
//
// If "from" and "to" query params are present, they are validated against the
// review's commit boundaries (base..head) and returned. Both must be present
// and from must precede to in the commit log.
//
// Otherwise falls back to sinceBase()/HeadSHA: "since=last_review" uses the
// last-reviewed SHA as the base; everything else uses the review's base_sha.
func (s *Server) diffRange(r *http.Request, rev *Review, repoPath string) (base, head string, err error) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from != "" || to != "" {
		if from == "" || to == "" {
			return "", "", fmt.Errorf("both from and to are required")
		}
		boundaries, bErr := CommitBoundaries(repoPath, rev.BaseSHA, rev.HeadSHA)
		if bErr != nil {
			return "", "", fmt.Errorf("could not compute boundaries: %w", bErr)
		}
		fi, ti := -1, -1
		for i, sha := range boundaries {
			if sha == from {
				fi = i
			}
			if sha == to {
				ti = i
			}
		}
		if fi < 0 || ti < 0 {
			return "", "", fmt.Errorf("from or to SHA not found in review commit range")
		}
		if fi >= ti {
			return "", "", fmt.Errorf("from must precede to in the commit log")
		}
		return from, to, nil
	}
	if r.URL.Query().Get("since") == "last_review" && rev.LastReviewedSHA != "" {
		return rev.LastReviewedSHA, rev.HeadSHA, nil
	}
	return rev.BaseSHA, rev.HeadSHA, nil
}

// --- projects ---

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ListProjects()
	if err != nil {
		writeErr(w, "database error", http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []*Project{}
	}
	writeJSON(w, projects)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r.PathValue("slug"))
	if p != nil {
		writeJSON(w, p)
	}
}

// --- reviews ---

// markMerged sets the computed Merged flag on each review (whether its head has
// landed on the base branch). Open reviews are skipped — they're not merged, and
// it avoids a git call per row.
func (s *Server) markMerged(p *Project, revs ...*Review) {
	for _, rev := range revs {
		if rev != nil && rev.Status != "open" {
			rev.Merged = IsMerged(p.RepoPath, rev.HeadSHA, p.BaseBranch)
		}
	}
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r.PathValue("slug"))
	if p == nil {
		return
	}
	branch := r.URL.Query().Get("branch")
	if branch != "" {
		rev, err := s.db.GetReviewByBranch(p.ID, branch)
		if err != nil {
			writeErr(w, "database error", http.StatusInternalServerError)
			return
		}
		if rev == nil {
			writeJSON(w, []*Review{})
			return
		}
		s.markMerged(p, rev)
		writeJSON(w, []*Review{rev})
		return
	}
	reviews, err := s.db.ListReviews(p.ID, r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, "database error", http.StatusInternalServerError)
		return
	}
	if reviews == nil {
		reviews = []*Review{}
	}
	s.markMerged(p, reviews...)
	writeJSON(w, reviews)
}

func (s *Server) syncBranch(p *Project, branch string) (*Review, error) {
	if !BranchExists(p.RepoPath, branch) {
		return nil, fmt.Errorf("branch %q not found in repo", branch)
	}
	headSHA, err := GetSHA(p.RepoPath, "refs/heads/"+branch)
	if err != nil || headSHA == "" {
		return nil, fmt.Errorf("could not resolve head of %q", branch)
	}
	baseSHA, err := GetMergeBase(p.RepoPath, p.BaseBranch, headSHA)
	if err != nil || baseSHA == "" {
		if !BranchExists(p.RepoPath, p.BaseBranch) {
			baseSHA = EmptyTreeSHA
		} else {
			return nil, fmt.Errorf("could not find merge base between %s and %s", p.BaseBranch, branch)
		}
	}

	prev, err := s.db.GetReviewByBranch(p.ID, branch)
	if err != nil {
		return nil, err
	}
	if prev != nil && prev.HeadSHA == headSHA {
		return prev, nil
	}

	rev, err := s.db.UpsertReview(p.ID, UpsertReviewParams{
		Branch:     branch,
		BaseBranch: p.BaseBranch,
		Title:      branch,
		HeadSHA:    headSHA,
		BaseSHA:    baseSHA,
	})
	if err != nil {
		return nil, err
	}
	if prev != nil && prev.HeadSHA != "" {
		s.retargetComments(p, rev, prev.HeadSHA, headSHA)
	}
	return rev, nil
}

func (s *Server) Resync() {
	projects, err := s.db.ListProjects()
	if err != nil {
		log.Printf("resync: %v", err)
		return
	}
	for _, p := range projects {
		heads, err := GetBranchHeads(p.RepoPath)
		if err != nil {
			log.Printf("resync %s: %v", p.Slug, err)
			continue
		}
		reviews, err := s.db.ListReviews(p.ID, "")
		if err != nil {
			log.Printf("resync %s: %v", p.Slug, err)
			continue
		}
		byBranch := make(map[string]*Review, len(reviews))
		for _, rev := range reviews {
			byBranch[rev.Branch] = rev
		}

		for branch, head := range heads {
			if branch == p.BaseBranch {
				continue
			}
			if rev := byBranch[branch]; rev != nil && rev.HeadSHA == head {
				continue
			}
			if _, err := s.syncBranch(p, branch); err != nil {
				log.Printf("resync %s/%s: %v", p.Slug, branch, err)
				continue
			}
			log.Printf("resync: synced %s/%s", p.Slug, branch)
		}

		for branch, rev := range byBranch {
			if rev.Status != "open" {
				continue
			}
			if _, ok := heads[branch]; ok {
				continue
			}
			if err := s.db.CloseReviewByBranch(p.ID, branch); err != nil {
				log.Printf("resync %s/%s: %v", p.Slug, branch, err)
				continue
			}
			log.Printf("resync: closed %s/%s (branch gone)", p.Slug, branch)
		}
	}
}

// --- hook ---

func (s *Server) postReceive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectSlug string `json:"project_slug"`
		Branch      string `json:"branch"`
		NewSHA      string `json:"new_sha"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	p := s.project(w, body.ProjectSlug)
	if p == nil {
		return
	}
	if IsZeroSHA(body.NewSHA) {
		if err := s.db.CloseReviewByBranch(p.ID, body.Branch); err != nil {
			writeErr(w, "database error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"action": "closed_on_delete"})
		return
	}
	if body.Branch == p.BaseBranch {
		writeJSON(w, map[string]string{"action": "ignored_base_branch"})
		return
	}
	rev, err := s.syncBranch(p, body.Branch)
	if err != nil {
		writeErr(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, rev, http.StatusCreated)
}

func (s *Server) retargetComments(p *Project, rev *Review, oldHead, newHead string) {
	comments, err := s.db.ListComments(rev.ID)
	if err != nil {
		log.Printf("retarget comments for review %d: %v", rev.ID, err)
		return
	}
	hunksByPath := map[string][]HunkRange{}
	for _, c := range comments {
		if c.FilePath == nil || c.LineNumber == nil {
			continue
		}
		path := *c.FilePath
		hunks, cached := hunksByPath[path]
		if !cached {
			text, dErr := GetPathDiff(p.RepoPath, oldHead, newHead, path)
			if dErr != nil {
				log.Printf("retarget %s in review %d: %v", path, rev.ID, dErr)
				continue
			}
			hunks = ParseHunkRanges(text)
			hunksByPath[path] = hunks
		}
		if len(hunks) == 0 {
			continue
		}

		start := int(*c.LineNumber)
		end := start
		if c.LineEnd != nil {
			end = int(*c.LineEnd)
		}
		newStart, newEnd := 0, 0
		for l := start; l <= end; l++ {
			if m := MapLine(hunks, l); m != 0 {
				if newStart == 0 {
					newStart = m
				}
				newEnd = m
			}
		}

		var anchorPath *string
		var anchorStart, anchorEnd *int64
		switch {
		case newStart != 0:
			anchorPath = &path
			s64 := int64(newStart)
			anchorStart = &s64
			if newEnd > newStart {
				e64 := int64(newEnd)
				anchorEnd = &e64
			}
		case BlobExists(p.RepoPath, newHead, path):
			anchorPath = &path
		}
		if anchorPath != nil && anchorStart != nil &&
			*anchorStart == *c.LineNumber && sameEnd(anchorEnd, c.LineEnd) {
			continue
		}
		if err := s.db.UpdateCommentAnchor(c.ID, anchorPath, anchorStart, anchorEnd); err != nil {
			log.Printf("retarget comment %d: %v", c.ID, err)
		}
	}
}

func sameEnd(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r.PathValue("slug"))
	if p == nil {
		return
	}
	rev := s.review(w, r.PathValue("id"), p)
	if rev == nil {
		return
	}
	s.markMerged(p, rev)
	writeJSON(w, rev)
}

// apiSettableStatuses are the review decisions a human can set through the API.
// The other two states are reached only by git events, not by this endpoint:
// "open" happens on push (UpsertReview) and "closed" on branch deletion
// (CloseReviewByBranch). Allowing them here would let a request reopen or close
// a review out of band, contradicting the documented state machine.
var apiSettableStatuses = map[string]bool{
	"approved": true, "changes_requested": true, "paused": true,
}

func (s *Server) updateReview(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r.PathValue("slug"))
	if p == nil {
		return
	}
	rev := s.review(w, r.PathValue("id"), p)
	if rev == nil {
		return
	}

	var body struct {
		Status *string `json:"status"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Status == nil {
		writeJSON(w, rev)
		return
	}
	if !apiSettableStatuses[*body.Status] {
		writeErr(w, "invalid status", http.StatusBadRequest)
		return
	}
	updated, err := s.db.UpdateReviewStatus(rev.ID, *body.Status)
	if err != nil {
		writeErr(w, "database error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, updated)
}

func (s *Server) getParsedDiff(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r.PathValue("slug"))
	if p == nil {
		return
	}
	rev := s.review(w, r.PathValue("id"), p)
	if rev == nil {
		return
	}
	highlight := r.URL.Query().Get("highlight") == "true"
	base, head, rangeErr := s.diffRange(r, rev, p.RepoPath)
	if rangeErr != nil {
		writeErr(w, rangeErr.Error(), http.StatusBadRequest)
		return
	}
	files, err := GetParsedDiff(p.RepoPath, base, head, highlight)
	if err != nil {
		writeErr(w, "diff failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if files == nil {
		files = []HighlightedFile{}
	}
	writeJSON(w, files)
}

func (s *Server) getFileContent(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r.PathValue("slug"))
	if p == nil {
		return
	}
	rev := s.review(w, r.PathValue("id"), p)
	if rev == nil {
		return
	}

	filePath := r.URL.Query().Get("path")
	sha := r.URL.Query().Get("sha")
	if filePath == "" || sha == "" {
		writeErr(w, "path and sha required", http.StatusBadRequest)
		return
	}
	boundaries, bErr := CommitBoundaries(p.RepoPath, rev.BaseSHA, rev.HeadSHA)
	if bErr != nil {
		writeErr(w, "could not resolve review boundaries", http.StatusInternalServerError)
		return
	}
	shaOK := false
	for _, b := range boundaries {
		if b == sha {
			shaOK = true
			break
		}
	}
	if !shaOK {
		writeErr(w, "sha not associated with this review", http.StatusBadRequest)
		return
	}

	start := 1
	count := 0
	if startStr := r.URL.Query().Get("start"); startStr != "" {
		if n, err := strconv.Atoi(startStr); err == nil && n > 0 {
			start = n
		}
	}
	if c := r.URL.Query().Get("count"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			count = n
		}
	}

	all, err := GetFileContent(p.RepoPath, sha, filePath)
	if err != nil {
		writeErr(w, "could not read file: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	lines := WindowLines(all, start, count)
	resp := map[string]any{"lines": lines, "start": start, "total": len(all)}
	if r.URL.Query().Get("highlight") == "true" {
		hlMap := HighlightFileContent(filePath, all)
		html := make([]string, len(lines))
		if hlMap != nil {
			for i := range lines {
				html[i] = hlMap[start+i]
			}
		}
		resp["html"] = html
	}
	writeJSON(w, resp)
}

func (s *Server) getCommits(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r.PathValue("slug"))
	if p == nil {
		return
	}
	rev := s.review(w, r.PathValue("id"), p)
	if rev == nil {
		return
	}
	commits, err := GetCommitLog(p.RepoPath, rev.BaseSHA, rev.HeadSHA)
	if err != nil {
		writeErr(w, "git error", http.StatusInternalServerError)
		return
	}
	if commits == nil {
		commits = []Commit{}
	}
	writeJSON(w, commits)
}

// --- comments ---

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r.PathValue("slug"))
	if p == nil {
		return
	}
	rev := s.review(w, r.PathValue("id"), p)
	if rev == nil {
		return
	}
	comments, err := s.db.ListComments(rev.ID)
	if err != nil {
		writeErr(w, "database error", http.StatusInternalServerError)
		return
	}
	if comments == nil {
		comments = []*Comment{}
	}
	writeJSON(w, comments)
}

func (s *Server) createComment(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r.PathValue("slug"))
	if p == nil {
		return
	}
	rev := s.review(w, r.PathValue("id"), p)
	if rev == nil {
		return
	}
	var body struct {
		Body       string `json:"body"`
		FilePath   string `json:"file_path"`
		LineNumber *int64 `json:"line_number"`
		LineEnd    *int64 `json:"line_end"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Body == "" {
		writeErr(w, "body required", http.StatusBadRequest)
		return
	}
	if body.LineEnd != nil {
		if body.LineNumber == nil || *body.LineEnd < *body.LineNumber {
			writeErr(w, "line_end must accompany line_number and not precede it", http.StatusBadRequest)
			return
		}
		if *body.LineEnd == *body.LineNumber {
			body.LineEnd = nil
		}
	}
	c, err := s.db.CreateComment(rev.ID, CreateCommentParams{
		Body:       body.Body,
		FilePath:   body.FilePath,
		LineNumber: body.LineNumber,
		LineEnd:    body.LineEnd,
	})
	if err != nil {
		writeErr(w, "database error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, c, http.StatusCreated)
}

func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r.PathValue("slug"))
	if p == nil {
		return
	}
	rev := s.review(w, r.PathValue("id"), p)
	if rev == nil {
		return
	}
	cid, err := strconv.ParseInt(r.PathValue("cid"), 10, 64)
	if err != nil {
		writeErr(w, "invalid comment id", http.StatusBadRequest)
		return
	}
	// Scope the delete to this review so a comment can't be deleted through an
	// unrelated project/review path.
	deleted, err := s.db.DeleteComment(rev.ID, cid)
	if err != nil {
		writeErr(w, "database error", http.StatusInternalServerError)
		return
	}
	if !deleted {
		writeErr(w, "comment not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
