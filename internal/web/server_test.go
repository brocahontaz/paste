package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"regexp"
	"strings"
	"testing"
	"time"

	"paste/internal/config"
	"paste/internal/ids"
	"paste/internal/store"
)

// newTestServer builds a Server over a fresh in-memory store and serves it
// via httptest. Each test gets an isolated server, store, and rate limiter.
func newTestServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	cfg := &config.Config{
		BindAddr:         ":0",
		DBPath:           ":memory:",
		MaxBodyBytes:     1 << 20,
		CreateRatePerMin: 5,
		ReadRatePerMin:   60,
		CleanupInterval:  time.Minute,
	}
	if mutate != nil {
		mutate(cfg)
	}
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	srv, err := New(cfg, st)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

func do(t *testing.T, method, url string, form url.Values) (*http.Response, string) {
	t.Helper()
	var resp *http.Response
	var err error
	if form == nil {
		req, reqErr := http.NewRequest(method, url, nil)
		if reqErr != nil {
			t.Fatalf("new request: %v", reqErr)
		}
		resp, err = http.DefaultClient.Do(req)
	} else {
		resp, err = http.DefaultClient.PostForm(url, form)
	}
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

// doXFF performs a request carrying an X-Forwarded-For header, mirroring do()
// for the header-carrying requests used in the proxy spoofing tests.
func doXFF(t *testing.T, method, url string, form url.Values, xff string) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("X-Forwarded-For", xff)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(respBody)
}

var (
	shareURLRe = regexp.MustCompile(`id="share-url"[^>]*value="([^"]*)"`)
	rawURLRe   = regexp.MustCompile(`id="raw-url"[^>]*value="([^"]*)"`)
	tokenRe    = regexp.MustCompile(`id="delete-token"[^>]*value="([^"]*)"`)
)

// createPaste posts a paste and extracts the ID, share URL, raw URL, and
// delete token from the result page. Extraction failures yield empty
// strings (callers that only need the status can ignore them).
func createPaste(t *testing.T, base string, content, format, language, expiry string, burn bool) (id, shareURL, rawURL, token string, resp *http.Response, body string) {
	t.Helper()
	form := url.Values{}
	form.Set("content", content)
	if format != "" {
		form.Set("format", format)
	}
	if language != "" {
		form.Set("language", language)
	}
	if expiry != "" {
		form.Set("expiry", expiry)
	}
	if burn {
		form.Set("burn", "1")
	}
	resp, body = do(t, http.MethodPost, base+"/", form)
	if m := shareURLRe.FindStringSubmatch(body); m != nil {
		shareURL = m[1]
		id = path.Base(shareURL)
	}
	if m := rawURLRe.FindStringSubmatch(body); m != nil {
		rawURL = m[1]
	}
	if m := tokenRe.FindStringSubmatch(body); m != nil {
		token = m[1]
	}
	return id, shareURL, rawURL, token, resp, body
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func TestCreateHappyPath(t *testing.T) {
	ts, _ := newTestServer(t, nil)

	id, share, raw, token, resp, body := createPaste(t, ts.URL, "hello world", "text", "", "1d", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d, body:\n%s", resp.StatusCode, body)
	}
	if !ids.ValidID(id) {
		t.Fatalf("id %q does not match ^[A-Za-z0-9]{10}$", id)
	}
	wantShare := ts.URL + "/" + id
	wantRaw := ts.URL + "/raw/" + id
	if share != wantShare {
		t.Errorf("share URL = %q, want %q", share, wantShare)
	}
	if raw != wantRaw {
		t.Errorf("raw URL = %q, want %q", raw, wantRaw)
	}
	if token == "" || len(token) < 40 {
		t.Errorf("delete token %q too short or missing", token)
	}
	if !strings.Contains(body, token) {
		t.Error("result page does not show the delete token")
	}
	// Three copy buttons: share URL, raw URL, delete token.
	if n := strings.Count(body, "data-copy="); n != 3 {
		t.Errorf("data-copy buttons = %d, want 3", n)
	}

	// The paste is viewable right after creation.
	vResp, vBody := do(t, http.MethodGet, wantShare, nil)
	if vResp.StatusCode != http.StatusOK || !strings.Contains(vBody, "hello world") {
		t.Fatalf("view after create: status %d, body:\n%s", vResp.StatusCode, vBody)
	}
}

func TestCreateBurnWarningOnResult(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	_, _, _, _, resp, body := createPaste(t, ts.URL, "secret", "text", "", "never", true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "destroyed immediately after the first view") {
		t.Error("result page missing burn warning")
	}
}

func TestCreateBurnShowsViewWarning(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	id, _, _, _, _, _ := createPaste(t, ts.URL, "secret", "text", "", "never", true)
	_, body := do(t, http.MethodGet, ts.URL+"/"+id, nil)
	if !strings.Contains(body, "This paste will be destroyed after this view") {
		t.Error("view page missing burn warning")
	}
}

// ---------------------------------------------------------------------------
// View: text, code, markdown
// ---------------------------------------------------------------------------

func TestViewText(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	id, _, _, _, _, _ := createPaste(t, ts.URL, "hello world\n<b>not html</b>", "text", "", "1d", false)

	resp, body := do(t, http.MethodGet, ts.URL+"/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("view status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "<pre class=\"text-view\" id=\"paste-content\">hello world") {
		t.Error("text paste not rendered in <pre>")
	}
	if !strings.Contains(body, "&lt;b&gt;not html&lt;/b&gt;") {
		t.Error("text content was not HTML-escaped")
	}
	if !strings.Contains(body, "Plain text") {
		t.Error("format label missing")
	}
	if strings.Contains(body, "Language:") {
		t.Error("language label shown for non-code paste")
	}
}

func TestViewCodeWithLanguage(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	id, _, _, _, _, _ := createPaste(t, ts.URL, "def main():\n    print('hi')\n", "code", "python", "1d", false)

	_, body := do(t, http.MethodGet, ts.URL+"/"+id, nil)
	if !strings.Contains(body, `class="hljs language-python"`) {
		t.Errorf("language class missing from code element:\n%s", body)
	}
	if !strings.Contains(body, "Source code") {
		t.Error("format label missing")
	}
	if !strings.Contains(body, "Python") {
		t.Error("language label missing")
	}
}

func TestViewCodeAutoDetect(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	id, _, _, _, _, _ := createPaste(t, ts.URL, "func main() {}", "code", "", "1d", false)

	_, body := do(t, http.MethodGet, ts.URL+"/"+id, nil)
	if !strings.Contains(body, `class="hljs"`) {
		t.Error("code element missing hljs class for auto-detect")
	}
	if strings.Contains(body, "language-") {
		t.Error("auto-detect paste should not pin a language class")
	}
	if !strings.Contains(body, "Auto-detect") {
		t.Error("language label should say Auto-detect")
	}
}

func TestViewMarkdownRendersAndEscapes(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	content := "**bold** and <script>alert(1)</script>"
	id, _, _, _, _, _ := createPaste(t, ts.URL, content, "markdown", "", "1d", false)

	_, body := do(t, http.MethodGet, ts.URL+"/"+id, nil)
	if !strings.Contains(body, "<strong>bold</strong>") {
		t.Error("markdown bold was not rendered")
	}
	if strings.Contains(body, "<script>") {
		t.Error("raw HTML leaked through markdown rendering")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("raw HTML was not escaped")
	}
	if !strings.Contains(body, "Markdown") {
		t.Error("format label missing")
	}
}

// ---------------------------------------------------------------------------
// Raw view
// ---------------------------------------------------------------------------

func TestRawExactBytesAndHeaders(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	content := "line1\nline2\ttabbed <>& \"quoted\"\ntrailing newline\n"
	id, _, _, _, _, _ := createPaste(t, ts.URL, content, "text", "", "1d", false)

	resp, body := do(t, http.MethodGet, ts.URL+"/raw/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("raw status = %d", resp.StatusCode)
	}
	if body != content {
		t.Errorf("raw body mismatch:\n got %q\nwant %q", body, content)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// ---------------------------------------------------------------------------
// Expiry
// ---------------------------------------------------------------------------

func TestExpiredPaste404AndPurgeRemovesRow(t *testing.T) {
	ts, st := newTestServer(t, nil)
	now := time.Now().Unix()
	past := now - 10
	err := st.Create(&store.Paste{
		ID: "expiredabc1", Content: []byte("gone soon"), Format: "text",
		DeleteTokenHash: ids.HashToken("tok"), CreatedAt: past, ExpiresAt: &past,
	})
	if err != nil {
		t.Fatalf("seed expired paste: %v", err)
	}

	// Expired reads are 404 (view and raw).
	if resp, _ := do(t, http.MethodGet, ts.URL+"/expiredabc1", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("expired view status = %d, want 404", resp.StatusCode)
	}
	if resp, _ := do(t, http.MethodGet, ts.URL+"/raw/expiredabc1", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("expired raw status = %d, want 404", resp.StatusCode)
	}

	// The purge loop's query removes the row.
	n, err := st.DeleteExpired(now)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n < 1 {
		t.Fatalf("purge removed %d rows, want >= 1", n)
	}
	if _, err := st.Get("expiredabc1"); !errors.Is(err, store.ErrNotFound) {
		t.Error("expired row still present after purge")
	}
}

func TestNeverExpirePersists(t *testing.T) {
	ts, st := newTestServer(t, nil)
	id, _, _, _, _, _ := createPaste(t, ts.URL, "stays forever", "text", "", "never", false)

	if resp, _ := do(t, http.MethodGet, ts.URL+"/"+id, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("view status = %d", resp.StatusCode)
	}
	// The purge loop must not touch a paste with no expiry.
	n, err := st.DeleteExpired(time.Now().Unix())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 0 {
		t.Fatalf("purge removed %d rows, want 0", n)
	}
	if resp, _ := do(t, http.MethodGet, ts.URL+"/"+id, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("view after purge status = %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Burn after read
// ---------------------------------------------------------------------------

func TestBurnViewFirstOKSecond404(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	id, _, _, _, _, _ := createPaste(t, ts.URL, "burn me", "text", "", "never", true)

	resp, _ := do(t, http.MethodGet, ts.URL+"/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first view status = %d, want 200", resp.StatusCode)
	}
	resp, _ = do(t, http.MethodGet, ts.URL+"/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second view status = %d, want 404", resp.StatusCode)
	}
	resp, _ = do(t, http.MethodGet, ts.URL+"/raw/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("raw after burn status = %d, want 404", resp.StatusCode)
	}
}

func TestBurnRawFirstOKSecond404(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	id, _, _, _, _, _ := createPaste(t, ts.URL, "burn raw", "code", "go", "never", true)

	resp, body := do(t, http.MethodGet, ts.URL+"/raw/"+id, nil)
	if resp.StatusCode != http.StatusOK || body != "burn raw" {
		t.Fatalf("first raw: status %d body %q", resp.StatusCode, body)
	}
	resp, _ = do(t, http.MethodGet, ts.URL+"/raw/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second raw status = %d, want 404", resp.StatusCode)
	}
	resp, _ = do(t, http.MethodGet, ts.URL+"/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("view after raw burn status = %d, want 404", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestDeleteFlow(t *testing.T) {
	ts, st := newTestServer(t, nil)
	id, _, _, token, _, _ := createPaste(t, ts.URL, "delete me", "text", "", "1d", false)

	// Confirmation page with valid token.
	resp, body := do(t, http.MethodGet, ts.URL+"/"+id+"/delete/"+token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, `action="/`+id+`/delete"`) || !strings.Contains(body, token) {
		t.Error("confirmation page missing POST form or token")
	}

	// Wrong token: rejected with 404 on both confirm and POST, paste survives.
	if resp, _ := do(t, http.MethodGet, ts.URL+"/"+id+"/delete/wrongtokenvalue123", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("confirm with wrong token status = %d, want 404", resp.StatusCode)
	}
	wrongForm := url.Values{"token": {"wrongtokenvalue123"}}
	if resp, _ := do(t, http.MethodPost, ts.URL+"/"+id+"/delete", wrongForm); resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete with wrong token status = %d, want 404", resp.StatusCode)
	}
	if resp, _ := do(t, http.MethodGet, ts.URL+"/"+id, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("paste should survive wrong-token delete, status = %d", resp.StatusCode)
	}

	// Only the hash is stored, never the plaintext token.
	p, err := st.Get(id)
	if err != nil {
		t.Fatalf("get for hash check: %v", err)
	}
	if p.DeleteTokenHash == token {
		t.Error("delete token stored in plaintext")
	}
	if len(p.DeleteTokenHash) != 64 {
		t.Errorf("stored hash length = %d, want 64", len(p.DeleteTokenHash))
	}

	// Valid token deletes; every later access is 404.
	form := url.Values{"token": {token}}
	if resp, _ := do(t, http.MethodPost, ts.URL+"/"+id+"/delete", form); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete with valid token status = %d", resp.StatusCode)
	}
	if resp, _ := do(t, http.MethodGet, ts.URL+"/"+id, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("view after delete status = %d, want 404", resp.StatusCode)
	}
	if resp, _ := do(t, http.MethodGet, ts.URL+"/"+id+"/delete/"+token, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("confirm after delete status = %d, want 404", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Validation and limits
// ---------------------------------------------------------------------------

func TestOversizedContent413(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	big := strings.Repeat("a", 2<<20) // 2 MiB > 1 MiB default limit
	form := url.Values{"content": {big}, "format": {"text"}}
	resp, _ := do(t, http.MethodPost, ts.URL+"/", form)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized create status = %d, want 413", resp.StatusCode)
	}
}

func TestEmptyContent400(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	form := url.Values{"content": {""}}
	resp, _ := do(t, http.MethodPost, ts.URL+"/", form)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty create status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateRateLimit429(t *testing.T) {
	ts, _ := newTestServer(t, nil) // default: 5 creates per minute
	form := url.Values{"content": {"x"}, "format": {"text"}}

	for i := 0; i < 5; i++ {
		resp, _ := do(t, http.MethodPost, ts.URL+"/", form)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("create %d status = %d, want 200", i+1, resp.StatusCode)
		}
	}
	resp, _ := do(t, http.MethodPost, ts.URL+"/", form)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("create 6 status = %d, want 429", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Error("429 missing Retry-After header")
	}
}

func TestReadRateLimit429(t *testing.T) {
	ts, _ := newTestServer(t, nil) // default: 60 reads per minute
	id, _, _, _, _, _ := createPaste(t, ts.URL, "hammer me", "text", "", "1d", false)
	if id == "" {
		t.Fatal("setup create failed")
	}

	for i := 0; i < 60; i++ {
		resp, _ := do(t, http.MethodGet, ts.URL+"/"+id, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("read %d status = %d, want 200", i+1, resp.StatusCode)
		}
	}
	resp, _ := do(t, http.MethodGet, ts.URL+"/"+id, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("read 61 status = %d, want 429", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Error("429 missing Retry-After header")
	}
}

// ---------------------------------------------------------------------------
// Security headers, robots, health, 404/405
// ---------------------------------------------------------------------------

func TestSecurityHeaders(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	resp, _ := do(t, http.MethodGet, ts.URL+"/", nil)

	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP = %q, want script-src 'self'", csp)
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Error("CSP contains unsafe-inline")
	}
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
		"X-Robots-Tag":           "noindex, nofollow",
	}
	for h, v := range want {
		if got := resp.Header.Get(h); got != v {
			t.Errorf("%s = %q, want %q", h, got, v)
		}
	}
}

func TestXRobotsTagAndViewMeta(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	id, _, _, _, _, _ := createPaste(t, ts.URL, "no robots here", "text", "", "1d", false)

	resp, body := do(t, http.MethodGet, ts.URL+"/"+id, nil)
	if got := resp.Header.Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Errorf("X-Robots-Tag on view = %q", got)
	}
	if !strings.Contains(body, `name="robots" content="noindex, nofollow"`) {
		t.Error("view page missing robots meta tag")
	}
}

func TestRobotsTxt(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	resp, body := do(t, http.MethodGet, ts.URL+"/robots.txt", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("robots status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "User-agent: *") || !strings.Contains(body, "Disallow: /") {
		t.Errorf("robots.txt body = %q", body)
	}
}

func TestHealthz(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	resp, body := do(t, http.MethodGet, ts.URL+"/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "ok") {
		t.Errorf("healthz body = %q", body)
	}
}

func TestUnknownRoutes404(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	for _, p := range []string{
		"/nope",
		"/short",
		"/AAAAAAAAAA",     // valid shape, unknown id
		"/raw/AAAAAAAAAA", // valid shape, unknown id
		"/a/b/c",
	} {
		resp, _ := do(t, http.MethodGet, ts.URL+p, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestWrongMethod405(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	for _, c := range []struct{ method, path string }{
		{http.MethodDelete, "/"},
		{http.MethodPut, "/"},
		{http.MethodPost, "/robots.txt"},
		{http.MethodPut, "/healthz"},
		{http.MethodDelete, "/raw/abcdefghij"},
	} {
		resp, _ := do(t, c.method, ts.URL+c.path, nil)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s status = %d, want 405", c.method, c.path, resp.StatusCode)
		}
		if allow := resp.Header.Get("Allow"); allow == "" {
			t.Errorf("%s %s missing Allow header", c.method, c.path)
		}
	}
}

// ---------------------------------------------------------------------------
// Base URL derivation
// ---------------------------------------------------------------------------

func TestConfiguredBaseURL(t *testing.T) {
	ts, _ := newTestServer(t, func(c *config.Config) {
		c.BaseURL = "https://p.example.com"
	})
	_, share, raw, _, resp, _ := createPaste(t, ts.URL, "custom base", "text", "", "1d", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	if !strings.HasPrefix(share, "https://p.example.com/") {
		t.Errorf("share URL = %q, want https://p.example.com/ prefix", share)
	}
	if !strings.HasPrefix(raw, "https://p.example.com/raw/") {
		t.Errorf("raw URL = %q", raw)
	}
}

func TestTrustProxyBaseURL(t *testing.T) {
	ts, _ := newTestServer(t, func(c *config.Config) {
		c.TrustProxy = true
	})
	id, _, _, _, _, _ := createPaste(t, ts.URL, "proxied", "text", "", "1d", false)
	if id == "" {
		t.Fatal("setup create failed")
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "https://"+req.URL.Host+"/raw/"+id) {
		t.Error("raw URL did not honor X-Forwarded-Proto with PASTE_TRUST_PROXY=true")
	}
}

// ---------------------------------------------------------------------------
// X-Forwarded-For spoofing
// ---------------------------------------------------------------------------

func TestXFFSpoofingIgnoredWhenTrustProxyFalse(t *testing.T) {
	ts, _ := newTestServer(t, nil) // default: TrustProxy=false, 5 creates/min
	form := url.Values{"content": {"x"}, "format": {"text"}}

	// Every create presents a fresh spoofed X-Forwarded-For to simulate
	// per-IP budget reset attempts, but all requests come from the same
	// RemoteAddr. The limiter must key on RemoteAddr and ignore the header.
	for i := 0; i < 5; i++ {
		resp, _ := doXFF(t, http.MethodPost, ts.URL+"/", form, fmt.Sprintf("10.0.0.%d", i+1))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("spoofed create %d status = %d, want 200", i+1, resp.StatusCode)
		}
	}
	resp, _ := doXFF(t, http.MethodPost, ts.URL+"/", form, "10.9.9.9")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("spoofed create 6 status = %d, want 429", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Error("429 missing Retry-After header")
	}
}

func TestXFFSpoofingFreshBudgetWhenTrustProxyTrue(t *testing.T) {
	ts, _ := newTestServer(t, func(c *config.Config) {
		c.TrustProxy = true
	})
	form := url.Values{"content": {"x"}, "format": {"text"}}

	// The inverse proves the switch actually switches: with TrustProxy=true
	// rotating X-Forwarded-For values DO yield fresh per-IP budgets, so all
	// six creates succeed.
	for i := 0; i < 6; i++ {
		resp, _ := doXFF(t, http.MethodPost, ts.URL+"/", form, fmt.Sprintf("10.0.0.%d", i+1))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("proxied create %d status = %d, want 200", i+1, resp.StatusCode)
		}
	}
}
