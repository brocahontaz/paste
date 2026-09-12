// Package web implements the paste HTTP server: routing, handlers,
// templates, embedded static assets, and middleware.
package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"runtime/debug"
	"strings"

	"paste"
	"paste/internal/config"
	"paste/internal/ratelimit"
	"paste/internal/store"
)

// csp is applied to every response. No 'unsafe-inline': all JS and CSS is
// served from /static/.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// templatePages are the page templates combined with base.html at startup.
var templatePages = []string{
	"home.html",
	"result.html",
	"view.html",
	"confirm_delete.html",
	"deleted.html",
	"error.html",
}

// Server holds all dependencies of the HTTP layer.
type Server struct {
	cfg         *config.Config
	store       *store.Store
	createLimit *ratelimit.Limiter
	readLimit   *ratelimit.Limiter
	pages       map[string]*template.Template
}

// New builds a Server. Template parsing errors surface here so a broken
// deployment fails fast at startup.
func New(cfg *config.Config, st *store.Store) (*Server, error) {
	tmplFS, err := fs.Sub(paste.Assets, "web/templates")
	if err != nil {
		return nil, fmt.Errorf("locate templates: %w", err)
	}
	pages := make(map[string]*template.Template, len(templatePages))
	for _, name := range templatePages {
		// Each page is parsed into its own template set together with
		// base.html so that page-level {{define}} blocks cannot collide.
		t, err := template.ParseFS(tmplFS, "base.html", name)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		pages[name] = t
	}
	return &Server{
		cfg:         cfg,
		store:       st,
		createLimit: ratelimit.New(cfg.CreateRatePerMin),
		readLimit:   ratelimit.New(cfg.ReadRatePerMin),
		pages:       pages,
	}, nil
}

// Handler returns the fully wired HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("POST /{$}", s.create)
	mux.HandleFunc("GET /raw/{id}", s.raw)
	mux.HandleFunc("GET /{id}/delete/{token}", s.confirmDelete)
	mux.HandleFunc("POST /{id}/delete", s.delete)
	mux.HandleFunc("GET /{id}", s.view)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /robots.txt", s.robots)
	// Catch-all for unmatched GET paths: a styled 404. Method mismatches on
	// known paths still yield ServeMux's 405 with an Allow header.
	mux.HandleFunc("GET /", s.notFound)

	// Static assets are dispatched before the mux: a registered "/static/"
	// prefix pattern would conflict with the wildcard "/{id}/delete/{token}"
	// route (e.g. "/static/delete/token" matches both).
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			s.static().ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return s.recoverPanics(secureHeaders(root))
}

// static serves embedded assets from /static/ with a modest cache lifetime.
func (s *Server) static() http.Handler {
	sub, _ := fs.Sub(paste.Assets, "web/static")
	files := http.StripPrefix("/static/", http.FileServerFS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		files.ServeHTTP(w, r)
	})
}

// secureHeaders applies the fixed security headers to every response.
// Cache-Control defaults to no-store; cacheable handlers (static assets)
// override it before writing.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// recoverPanics converts handler panics into a styled 500 page.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic serving %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				s.renderError(w, http.StatusInternalServerError, "Internal server error.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// render executes a page template into a buffer first, so template failures
// produce a clean 500 instead of a partially written page.
func (s *Server) render(w http.ResponseWriter, code int, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base.html", data); err != nil {
		log.Printf("render %s: %v", page, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(buf.Bytes())
}

type errorData struct {
	Code    int
	Message string
}

// renderError renders the styled error page with the given status code.
func (s *Server) renderError(w http.ResponseWriter, code int, msg string) {
	s.render(w, code, "error.html", errorData{Code: code, Message: msg})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	// Generic message: unknown paths, unknown IDs, and expired pastes are
	// indistinguishable to callers.
	s.renderError(w, http.StatusNotFound, "This page does not exist, or the paste is unknown or expired.")
}
