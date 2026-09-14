package internal

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"marcparadise.io/projects/reviewer/web"
)

type Server struct {
	cfg *Config
	db  *DB
}

func New(cfg *Config, database *DB) *Server {
	return &Server{cfg: cfg, db: database}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static — catch-all, more-specific API routes take precedence
	webFS, err := fs.Sub(web.Files, ".")
	if err != nil {
		panic(err)
	}
	// Embedded assets carry no version hash and zero modtime, so a browser would
	// otherwise heuristically cache them and keep serving the old frontend after
	// a server upgrade. no-cache forces revalidation on every load.
	mux.Handle("GET /", noCache(http.FileServer(http.FS(webFS))))

	// Generated syntax-highlighting stylesheet (more specific than the catch-all)
	mux.HandleFunc("GET /highlight.css", s.highlightCSS)

	// Projects
	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("GET /api/projects/{slug}", s.getProject)

	// Reviews
	mux.HandleFunc("GET /api/projects/{slug}/reviews", s.listReviews)
	mux.HandleFunc("GET /api/projects/{slug}/reviews/{id}", s.getReview)
	mux.HandleFunc("PATCH /api/projects/{slug}/reviews/{id}", s.updateReview)
	mux.HandleFunc("GET /api/projects/{slug}/reviews/{id}/diff/parsed", s.getParsedDiff)
	mux.HandleFunc("GET /api/projects/{slug}/reviews/{id}/file-content", s.getFileContent)
	mux.HandleFunc("GET /api/projects/{slug}/reviews/{id}/commits", s.getCommits)

	// Comments
	mux.HandleFunc("GET /api/projects/{slug}/reviews/{id}/comments", s.listComments)
	mux.HandleFunc("POST /api/projects/{slug}/reviews/{id}/comments", s.createComment)
	mux.HandleFunc("DELETE /api/projects/{slug}/reviews/{id}/comments/{cid}", s.deleteComment)

	// Git hook
	mux.HandleFunc("POST /api/hooks/post-receive", s.postReceive)

	return logging(s.guard(mux))
}

func (s *Server) StartResync(interval time.Duration) {
	if interval <= 0 {
		log.Printf("periodic resync disabled")
		return
	}
	s.Resync()
	go func() {
		for range time.NewTicker(interval).C {
			s.Resync()
		}
	}()
	log.Printf("periodic resync every %s", interval)
}

func (s *Server) ListenAndServe() error {
	addr := s.cfg.BindAddr()
	log.Printf("reviewer listening on http://%s", addr)
	return http.ListenAndServe(addr, s.Handler())
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

// guard enforces the loopback trust model at the HTTP boundary. The server is
// same-origin and loopback-only, so the only realistic remote attacker is a web
// page open in the operator's browser. Two checks close that surface:
//
//   - Host header (all methods): the request's Host must be one of our loopback
//     names at our port. This defeats DNS-rebinding, where a page at a hostile
//     domain re-points DNS at 127.0.0.1 to reach us with its own Origin.
//   - Origin + Content-Type (state-changing methods): a cross-site page can fire
//     a "simple" POST/PATCH/DELETE without a preflight (CSRF); requiring an
//     application/json body and rejecting any foreign Origin blocks it while
//     leaving same-origin fetches, the hook's curl, and the CLI untouched (they
//     send either a matching Origin or none at all).
//
// There is no CORS: the UI is served from the same origin and never needs it,
// and a permissive CORS policy would only hand API responses to other sites.
func (s *Server) guard(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, h := range []string{"127.0.0.1", "localhost", "[::1]"} {
		allowed[fmt.Sprintf("%s:%d", h, s.cfg.Port)] = true
	}
	isStateChanging := func(m string) bool {
		return m == http.MethodPost || m == http.MethodPatch || m == http.MethodDelete
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			writeErr(w, "forbidden host", http.StatusForbidden)
			return
		}
		if isStateChanging(r.Method) {
			if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(origin, allowed) {
				writeErr(w, "forbidden origin", http.StatusForbidden)
				return
			}
			// Enforce a JSON content type only when a body is present. Bodyless
			// mutations (the UI's DELETE requests send no Content-Type) are still
			// covered by the Host and Origin checks above; the check exists to
			// reject a cross-site "simple" POST, which always carries a body.
			if r.ContentLength != 0 {
				if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
					writeErr(w, "unsupported content type", http.StatusUnsupportedMediaType)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed reports whether an Origin header's host:port is in the allowlist.
// A malformed Origin (no host) is rejected.
func originAllowed(origin string, allowed map[string]bool) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return allowed[u.Host]
}
