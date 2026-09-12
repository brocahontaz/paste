package web

import (
	"crypto/subtle"
	"errors"
	"html/template"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"paste/internal/ids"
	"paste/internal/ratelimit"
	"paste/internal/store"
)

// ---------------------------------------------------------------------------
// Form option data
// ---------------------------------------------------------------------------

type selectOption struct {
	Value    string
	Label    string
	Selected bool
}

var formatOptions = []selectOption{
	{Value: "text", Label: "Plain text", Selected: true},
	{Value: "markdown", Label: "Markdown"},
	{Value: "code", Label: "Source code"},
}

// languageOptions are highlight.js language names served by the vendored
// common build (web/static/highlight.min.js). "" means auto-detection.
var languageOptions = []selectOption{
	{Value: "", Label: "Auto-detect", Selected: true},
	{Value: "plaintext", Label: "Plain text"},
	{Value: "bash", Label: "Bash"},
	{Value: "shell", Label: "Shell"},
	{Value: "c", Label: "C"},
	{Value: "cpp", Label: "C++"},
	{Value: "csharp", Label: "C#"},
	{Value: "css", Label: "CSS"},
	{Value: "diff", Label: "Diff"},
	{Value: "go", Label: "Go"},
	{Value: "graphql", Label: "GraphQL"},
	{Value: "ini", Label: "INI / TOML"},
	{Value: "java", Label: "Java"},
	{Value: "javascript", Label: "JavaScript"},
	{Value: "json", Label: "JSON"},
	{Value: "kotlin", Label: "Kotlin"},
	{Value: "lua", Label: "Lua"},
	{Value: "makefile", Label: "Makefile"},
	{Value: "markdown", Label: "Markdown"},
	{Value: "objectivec", Label: "Objective-C"},
	{Value: "perl", Label: "Perl"},
	{Value: "php", Label: "PHP"},
	{Value: "python", Label: "Python"},
	{Value: "r", Label: "R"},
	{Value: "ruby", Label: "Ruby"},
	{Value: "rust", Label: "Rust"},
	{Value: "sql", Label: "SQL"},
	{Value: "swift", Label: "Swift"},
	{Value: "typescript", Label: "TypeScript"},
	{Value: "xml", Label: "HTML / XML"},
	{Value: "yaml", Label: "YAML"},
}

func formatLabel(f string) string {
	for _, o := range formatOptions {
		if o.Value == f {
			return o.Label
		}
	}
	return f
}

func languageLabel(l string) string {
	for _, o := range languageOptions {
		if o.Value == l {
			return o.Label
		}
	}
	return l
}

// normalizeFormat returns v if it is a known format, else a safe default
// ("text"). Chosen over a 400 because the value has no security impact and
// the form only offers known values; a garbage submission still yields a
// usable paste.
func normalizeFormat(v string) string {
	for _, o := range formatOptions {
		if o.Value == v {
			return v
		}
	}
	return "text"
}

// normalizeLanguage returns v if it is a known highlight.js language, else
// "" (auto-detect). Safe default: worst case the client highlights nothing.
func normalizeLanguage(v string) string {
	if v == "" {
		return ""
	}
	for _, o := range languageOptions {
		if o.Value == v {
			return v
		}
	}
	return ""
}

// expiryFromForm maps a select value to an absolute unix expiry, or nil for
// "never". Unknown values fall back to 7 days (safe default; documented
// choice — the form only offers the known values).
func expiryFromForm(v string, now int64) *int64 {
	var d time.Duration
	switch v {
	case "1h":
		d = time.Hour
	case "1d":
		d = 24 * time.Hour
	case "7d":
		d = 7 * 24 * time.Hour
	case "30d":
		d = 30 * 24 * time.Hour
	case "never":
		return nil
	default:
		d = 7 * 24 * time.Hour
	}
	t := now + int64(d/time.Second)
	return &t
}

// ---------------------------------------------------------------------------
// Client IP and base URL derivation
// ---------------------------------------------------------------------------

// clientIP keys the rate limiters. It only honors X-Forwarded-For (first
// hop) when PASTE_TRUST_PROXY is enabled, because a spoofed header would
// otherwise let clients evade or exhaust limits.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// baseURL returns PASTE_BASE_URL when set, else derives scheme://host from
// the request. With PASTE_TRUST_PROXY the scheme honors X-Forwarded-Proto.
func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if s.cfg.TrustProxy {
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		}
	}
	return scheme + "://" + r.Host
}

// ---------------------------------------------------------------------------
// Rate limiting helper
// ---------------------------------------------------------------------------

// limit applies a rate limiter for key; on denial it writes a styled 429
// with a Retry-After header and returns false.
func (s *Server) limit(w http.ResponseWriter, key string, l *ratelimit.Limiter) bool {
	retry, ok := l.Allow(key)
	if ok {
		return true
	}
	secs := int(math.Ceil(retry.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	s.renderError(w, http.StatusTooManyRequests, "Too many requests. Please try again later.")
	return false
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

type homeData struct {
	Formats   []selectOption
	Languages []selectOption
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "home.html", homeData{Formats: formatOptions, Languages: languageOptions})
}

type resultData struct {
	ID       string
	ShareURL string
	RawURL   string
	Token    string
	Burn     bool
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.cfg.TrustProxy)
	if !s.limit(w, ip, s.createLimit) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	if err := r.ParseForm(); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.renderError(w, http.StatusRequestEntityTooLarge, "The paste exceeds the maximum allowed size.")
			return
		}
		s.renderError(w, http.StatusBadRequest, "Invalid form submission.")
		return
	}

	content := r.Form.Get("content")
	if content == "" {
		s.renderError(w, http.StatusBadRequest, "Content must not be empty.")
		return
	}
	if int64(len(content)) > s.cfg.MaxBodyBytes {
		s.renderError(w, http.StatusRequestEntityTooLarge, "The paste exceeds the maximum allowed size.")
		return
	}

	format := normalizeFormat(r.Form.Get("format"))
	language := normalizeLanguage(r.Form.Get("language"))
	burn := r.Form.Get("burn") == "1"
	now := time.Now().Unix()
	expiresAt := expiryFromForm(r.Form.Get("expiry"), now)

	var (
		id, token string
		created   bool
	)
	for attempt := 0; attempt < 5; attempt++ {
		newID, err := ids.NewID()
		if err != nil {
			log.Printf("generate id: %v", err)
			s.renderError(w, http.StatusInternalServerError, "Internal server error.")
			return
		}
		newToken, err := ids.NewDeleteToken()
		if err != nil {
			log.Printf("generate token: %v", err)
			s.renderError(w, http.StatusInternalServerError, "Internal server error.")
			return
		}
		p := &store.Paste{
			ID:              newID,
			Content:         []byte(content),
			Format:          format,
			Language:        language,
			Burn:            burn,
			DeleteTokenHash: ids.HashToken(newToken),
			CreatedAt:       now,
			ExpiresAt:       expiresAt,
		}
		err = s.store.Create(p)
		if errors.Is(err, store.ErrConflict) {
			continue // PK collision: retry with a fresh ID
		}
		if err != nil {
			log.Printf("store create: %v", err)
			s.renderError(w, http.StatusInternalServerError, "Internal server error.")
			return
		}
		id, token, created = newID, newToken, true
		break
	}
	if !created {
		log.Printf("create: exhausted id retries")
		s.renderError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}

	base := s.baseURL(r)
	s.render(w, http.StatusOK, "result.html", resultData{
		ID:       id,
		ShareURL: base + "/" + id,
		RawURL:   base + "/raw/" + id,
		Token:    token,
		Burn:     burn,
	})
}

type viewData struct {
	ID            string
	Format        string
	FormatLabel   string
	Language      string
	LanguageLabel string
	Content       string
	Rendered      template.HTML // markdown only; produced by goldmark with raw HTML escaped
	Burn          bool
	ExpiryLabel   string
	RawURL        string
}

func expiryLabel(p *store.Paste) string {
	if p.ExpiresAt == nil {
		return "Never expires"
	}
	return "Expires " + time.Unix(*p.ExpiresAt, 0).UTC().Format("2006-01-02 15:04 UTC")
}

func (s *Server) view(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ids.ValidID(id) {
		s.notFound(w, r)
		return
	}
	if !s.limit(w, clientIP(r, s.cfg.TrustProxy), s.readLimit) {
		return
	}
	p, err := s.store.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		log.Printf("store get: %v", err)
		s.renderError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}

	data := &viewData{
		ID:            p.ID,
		Format:        p.Format,
		FormatLabel:   formatLabel(p.Format),
		Language:      p.Language,
		LanguageLabel: languageLabel(p.Language),
		Content:       string(p.Content),
		Burn:          p.Burn,
		ExpiryLabel:   expiryLabel(p),
		RawURL:        s.baseURL(r) + "/raw/" + p.ID,
	}
	if p.Format == "markdown" {
		rendered, err := renderMarkdown(p.Content)
		if err != nil {
			log.Printf("render markdown: %v", err)
			s.renderError(w, http.StatusInternalServerError, "Internal server error.")
			return
		}
		data.Rendered = rendered
	}
	s.render(w, http.StatusOK, "view.html", data)

	// Burn after read: the paste is destroyed once the view response has
	// been written. The next retrieval gets a 404.
	if p.Burn {
		if err := s.store.DeleteByID(id); err != nil {
			log.Printf("burn delete %s: %v", id, err)
		}
	}
}

func (s *Server) raw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ids.ValidID(id) {
		s.notFound(w, r)
		return
	}
	if !s.limit(w, clientIP(r, s.cfg.TrustProxy), s.readLimit) {
		return
	}
	p, err := s.store.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		log.Printf("store get: %v", err)
		s.renderError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(p.Content)

	// Burn after read applies to raw retrieval as well.
	if p.Burn {
		if err := s.store.DeleteByID(id); err != nil {
			log.Printf("burn delete %s: %v", id, err)
		}
	}
}

// tokenMatches compares the submitted token against the stored hash in
// constant time.
func tokenMatches(storedHash, token string) bool {
	computed := ids.HashToken(token)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}

type confirmData struct {
	ID    string
	Token string
}

func (s *Server) confirmDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	token := r.PathValue("token")
	if !ids.ValidID(id) || token == "" {
		s.notFound(w, r)
		return
	}
	if !s.limit(w, clientIP(r, s.cfg.TrustProxy), s.readLimit) {
		return
	}
	p, err := s.store.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		log.Printf("store get: %v", err)
		s.renderError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}
	// Wrong token and unknown ID are both 404: existence is never leaked.
	if !tokenMatches(p.DeleteTokenHash, token) {
		s.notFound(w, r)
		return
	}
	s.render(w, http.StatusOK, "confirm_delete.html", confirmData{ID: id, Token: token})
}

type deletedData struct {
	ID string
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ids.ValidID(id) {
		s.notFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10) // tiny form: id + token
	if err := r.ParseForm(); err != nil {
		s.renderError(w, http.StatusBadRequest, "Invalid form submission.")
		return
	}
	token := r.Form.Get("token")
	if token == "" {
		s.notFound(w, r)
		return
	}
	// Wrong token and unknown ID are both 404: existence is never leaked.
	err := s.store.DeleteWithToken(id, ids.HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		log.Printf("store delete: %v", err)
		s.renderError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}
	s.render(w, http.StatusOK, "deleted.html", deletedData{ID: id})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := s.store.Ping(); err != nil {
		log.Printf("healthz ping: %v", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "unhealthy")
		return
	}
	_, _ = io.WriteString(w, "ok")
}

func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "User-agent: *\nDisallow: /\n")
}
