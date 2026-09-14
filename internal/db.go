package internal

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS projects (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	slug        TEXT    UNIQUE NOT NULL,
	repo_path   TEXT    NOT NULL,
	base_branch TEXT    NOT NULL DEFAULT 'main',
	created_at  TEXT    DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS reviews (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id  INTEGER NOT NULL REFERENCES projects(id),
	branch      TEXT    NOT NULL,
	base_branch TEXT    NOT NULL,
	title       TEXT    NOT NULL DEFAULT '',
	status      TEXT    NOT NULL DEFAULT 'open',
	head_sha    TEXT    NOT NULL DEFAULT '',
	base_sha              TEXT    NOT NULL DEFAULT '',
	last_reviewed_sha     TEXT    NOT NULL DEFAULT '',
	last_opened_at        TEXT    NOT NULL DEFAULT '',
	created_at  TEXT    DEFAULT (datetime('now')),
	updated_at  TEXT    DEFAULT (datetime('now')),
	UNIQUE(project_id, branch)
);
CREATE TABLE IF NOT EXISTS comments (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	review_id   INTEGER NOT NULL REFERENCES reviews(id),
	body        TEXT    NOT NULL,
	file_path   TEXT,
	line_number INTEGER,
	line_end    INTEGER,
	created_at  TEXT    DEFAULT (datetime('now'))
);
`

type DB struct {
	sql *sql.DB
}

type Project struct {
	ID         int64  `json:"id"`
	Slug       string `json:"slug"`
	RepoPath   string `json:"repo_path"`
	BaseBranch string `json:"base_branch"`
	CreatedAt  string `json:"created_at"`
}

type Review struct {
	ID              int64  `json:"id"`
	ProjectID       int64  `json:"project_id"`
	Branch          string `json:"branch"`
	BaseBranch      string `json:"base_branch"`
	Title           string `json:"title"`
	Status          string `json:"status"`
	HeadSHA         string `json:"head_sha"`
	BaseSHA         string `json:"base_sha"`
	LastReviewedSHA string `json:"last_reviewed_sha"`
	LastOpenedAt    string `json:"last_opened_at"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`

	// Merged is computed (not persisted): whether the branch's head has landed
	// on the base branch. Populated by handlers before serialization.
	Merged bool `json:"merged"`
}

type Comment struct {
	ID         int64   `json:"id"`
	ReviewID   int64   `json:"review_id"`
	Body       string  `json:"body"`
	FilePath   *string `json:"file_path"`
	LineNumber *int64  `json:"line_number"`
	LineEnd    *int64  `json:"line_end"`
	CreatedAt  string  `json:"created_at"`
}

type scanner interface {
	Scan(dest ...any) error
}

func Open(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", path+"?_pragma=journal_mode%3DWAL&_pragma=foreign_keys%3DON&_pragma=busy_timeout%3D5000")
	if err != nil {
		return nil, err
	}
	sqldb.SetMaxOpenConns(1)
	d := &DB{sql: sqldb}
	if _, err := sqldb.Exec(schema); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// --- Projects ---

func (d *DB) CreateProject(slug, repoPath, baseBranch string) (*Project, error) {
	_, err := d.sql.Exec(
		`INSERT INTO projects (slug, repo_path, base_branch) VALUES (?, ?, ?)`,
		slug, repoPath, baseBranch,
	)
	if err != nil {
		return nil, err
	}
	return d.GetProject(slug)
}

func (d *DB) GetProject(slug string) (*Project, error) {
	row := d.sql.QueryRow(
		`SELECT id, slug, repo_path, base_branch, created_at FROM projects WHERE slug = ?`, slug,
	)
	var p Project
	err := row.Scan(&p.ID, &p.Slug, &p.RepoPath, &p.BaseBranch, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &p, err
}

func (d *DB) UpdateProjectSlug(id int64, slug string) error {
	_, err := d.sql.Exec(`UPDATE projects SET slug = ? WHERE id = ?`, slug, id)
	return err
}

func (d *DB) ListProjects() ([]*Project, error) {
	rows, err := d.sql.Query(
		`SELECT id, slug, repo_path, base_branch, created_at FROM projects ORDER BY slug`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Slug, &p.RepoPath, &p.BaseBranch, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// --- Reviews ---

const reviewCols = `id, project_id, branch, base_branch, title, status, head_sha, base_sha, last_reviewed_sha, last_opened_at, created_at, updated_at`

func scanReview(s scanner) (*Review, error) {
	var r Review
	err := s.Scan(&r.ID, &r.ProjectID, &r.Branch, &r.BaseBranch, &r.Title,
		&r.Status, &r.HeadSHA, &r.BaseSHA, &r.LastReviewedSHA, &r.LastOpenedAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

type UpsertReviewParams struct {
	Branch     string
	BaseBranch string
	Title      string
	HeadSHA    string
	BaseSHA    string
}

func (d *DB) UpsertReview(projectID int64, p UpsertReviewParams) (*Review, error) {
	res, err := d.sql.Exec(
		`UPDATE reviews SET head_sha=?, base_sha=?, status='open', last_opened_at=datetime('now'), updated_at=datetime('now') WHERE project_id=? AND branch=?`,
		p.HeadSHA, p.BaseSHA, projectID, p.Branch,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := d.sql.Exec(
			`INSERT INTO reviews (project_id, branch, base_branch, title, head_sha, base_sha, last_opened_at)
			 VALUES (?, ?, ?, ?, ?, ?, datetime('now'))`,
			projectID, p.Branch, p.BaseBranch, p.Title, p.HeadSHA, p.BaseSHA,
		); err != nil {
			return nil, err
		}
	}
	return d.GetReviewByBranch(projectID, p.Branch)
}

func (d *DB) GetReview(id int64) (*Review, error) {
	row := d.sql.QueryRow(`SELECT `+reviewCols+` FROM reviews WHERE id = ?`, id)
	r, err := scanReview(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

func (d *DB) GetReviewByBranch(projectID int64, branch string) (*Review, error) {
	row := d.sql.QueryRow(
		`SELECT `+reviewCols+` FROM reviews WHERE project_id=? AND branch=?`, projectID, branch,
	)
	r, err := scanReview(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

func (d *DB) ListReviews(projectID int64, statusFilter string) ([]*Review, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if statusFilter != "" {
		rows, err = d.sql.Query(
			`SELECT `+reviewCols+` FROM reviews WHERE project_id=? AND status=? ORDER BY updated_at DESC`,
			projectID, statusFilter,
		)
	} else {
		rows, err = d.sql.Query(
			`SELECT `+reviewCols+` FROM reviews WHERE project_id=? ORDER BY updated_at DESC`,
			projectID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Review
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateReviewStatus records a human review decision. It also snapshots the
// current head SHA so "since last review" diffs can be computed later.
func (d *DB) UpdateReviewStatus(id int64, status string) (*Review, error) {
	q := `UPDATE reviews SET status = ?, last_reviewed_sha = head_sha, updated_at = datetime('now') WHERE id = ?`
	if _, err := d.sql.Exec(q, status, id); err != nil {
		return nil, err
	}
	return d.GetReview(id)
}

func (d *DB) CloseReviewByBranch(projectID int64, branch string) error {
	_, err := d.sql.Exec(
		`UPDATE reviews SET status='closed', updated_at=datetime('now') WHERE project_id=? AND branch=? AND status='open'`,
		projectID, branch,
	)
	return err
}

func (d *DB) CountReviews(projectID int64, statusFilter string) (int, error) {
	var n int
	var err error
	if statusFilter != "" {
		err = d.sql.QueryRow(
			`SELECT count(*) FROM reviews WHERE project_id=? AND status=?`, projectID, statusFilter,
		).Scan(&n)
	} else {
		err = d.sql.QueryRow(
			`SELECT count(*) FROM reviews WHERE project_id=?`, projectID,
		).Scan(&n)
	}
	return n, err
}

// --- Comments ---

const commentCols = `id, review_id, body, file_path, line_number, line_end, created_at`

func scanComment(s scanner) (*Comment, error) {
	var c Comment
	var fp sql.NullString
	var ln, le sql.NullInt64
	err := s.Scan(&c.ID, &c.ReviewID, &c.Body, &fp, &ln, &le, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	if fp.Valid {
		c.FilePath = &fp.String
	}
	if ln.Valid {
		c.LineNumber = &ln.Int64
	}
	if le.Valid {
		c.LineEnd = &le.Int64
	}
	return &c, nil
}

type CreateCommentParams struct {
	Body       string
	FilePath   string
	LineNumber *int64
	LineEnd    *int64
}

func (d *DB) CreateComment(reviewID int64, p CreateCommentParams) (*Comment, error) {
	var fp interface{}
	if p.FilePath != "" {
		fp = p.FilePath
	}
	var ln, le interface{}
	if p.LineNumber != nil {
		ln = *p.LineNumber
	}
	if p.LineEnd != nil {
		le = *p.LineEnd
	}
	res, err := d.sql.Exec(
		`INSERT INTO comments (review_id, body, file_path, line_number, line_end) VALUES (?, ?, ?, ?, ?)`,
		reviewID, p.Body, fp, ln, le,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	row := d.sql.QueryRow(`SELECT `+commentCols+` FROM comments WHERE id=?`, id)
	return scanComment(row)
}

func (d *DB) UpdateCommentAnchor(id int64, filePath *string, lineNumber, lineEnd *int64) error {
	var fp, ln, le interface{}
	if filePath != nil {
		fp = *filePath
	}
	if lineNumber != nil {
		ln = *lineNumber
	}
	if lineEnd != nil {
		le = *lineEnd
	}
	_, err := d.sql.Exec(
		`UPDATE comments SET file_path=?, line_number=?, line_end=? WHERE id=?`,
		fp, ln, le, id,
	)
	return err
}

func (d *DB) ListComments(reviewID int64) ([]*Comment, error) {
	rows, err := d.sql.Query(
		`SELECT `+commentCols+` FROM comments WHERE review_id=? ORDER BY created_at`, reviewID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) ListCommentsSince(reviewID int64, since string) ([]*Comment, error) {
	rows, err := d.sql.Query(
		`SELECT `+commentCols+` FROM comments WHERE review_id=? AND created_at >= ? ORDER BY created_at`,
		reviewID, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteComment removes the comment only if it belongs to reviewID, and reports
// whether a row was actually deleted (false = no such comment under that review).
func (d *DB) DeleteComment(reviewID, id int64) (bool, error) {
	res, err := d.sql.Exec(`DELETE FROM comments WHERE id=? AND review_id=?`, id, reviewID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
