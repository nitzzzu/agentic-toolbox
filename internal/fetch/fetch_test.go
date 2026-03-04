package fetch

// Internal (whitebox) test file — has access to unexported functions:
// setBrowserHeaders, getBrowserClient, newBrowserTransport, proxyURLFor, cacheKey.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// setBrowserHeaders
// ---------------------------------------------------------------------------

func TestSetBrowserHeaders_allPresent(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	setBrowserHeaders(req)

	checks := [][2]string{
		{"User-Agent", "Chrome/132"},
		{"Accept", "text/html"},
		{"Accept-Language", "en-US"},
		{"Sec-CH-UA", "Chrome"},
		{"Sec-CH-UA-Mobile", "?0"},
		{"Sec-CH-UA-Platform", "Windows"},
		{"Sec-Fetch-Dest", "document"},
		{"Sec-Fetch-Mode", "navigate"},
		{"Sec-Fetch-Site", "none"},
		{"Sec-Fetch-User", "?1"},
		{"Upgrade-Insecure-Requests", "1"},
		{"Cache-Control", "max-age=0"},
	}
	for _, chk := range checks {
		got := req.Header.Get(chk[0])
		if !strings.Contains(got, chk[1]) {
			t.Errorf("%s: want to contain %q, got %q", chk[0], chk[1], got)
		}
	}
}

func TestSetBrowserHeaders_noAcceptEncoding(t *testing.T) {
	// Go's Transport adds gzip automatically; setting it here would require
	// manual brotli decompression. Must stay unset.
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	setBrowserHeaders(req)
	if enc := req.Header.Get("Accept-Encoding"); enc != "" {
		t.Errorf("Accept-Encoding must not be set manually, got %q", enc)
	}
}

func TestSetBrowserHeaders_secCHUABrands(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	setBrowserHeaders(req)
	chua := req.Header.Get("Sec-CH-UA")
	if !strings.Contains(chua, `"Google Chrome"`) {
		t.Errorf("Sec-CH-UA: want Google Chrome brand, got %q", chua)
	}
	if !strings.Contains(chua, "v=") || !strings.Contains(chua, "132") {
		t.Errorf("Sec-CH-UA: want version 132, got %q", chua)
	}
}

// ---------------------------------------------------------------------------
// getBrowserClient — singleton + cookie jar
// ---------------------------------------------------------------------------

func TestGetBrowserClient_singleton(t *testing.T) {
	c1 := getBrowserClient()
	c2 := getBrowserClient()
	if c1 == nil {
		t.Fatal("getBrowserClient returned nil")
	}
	if c1 != c2 {
		t.Error("getBrowserClient must return the same *http.Client (sync.Once singleton)")
	}
}

func TestGetBrowserClient_hasCookieJar(t *testing.T) {
	c := getBrowserClient()
	if c.Jar == nil {
		t.Error("browser client must have a cookie jar for session continuity across redirects")
	}
}

func TestGetBrowserClient_hasTimeout(t *testing.T) {
	c := getBrowserClient()
	if c.Timeout == 0 {
		t.Error("browser client must have a non-zero timeout")
	}
}

// ---------------------------------------------------------------------------
// newBrowserTransport — HTTP/2 disabled, uTLS dialer wired
// ---------------------------------------------------------------------------

func TestNewBrowserTransport_http2Disabled(t *testing.T) {
	// TLSNextProto: make(...) prevents Go from upgrading to h2 even if ALPN
	// somehow returns "h2" — belt-and-suspenders guard alongside the ALPN fix.
	tr := newBrowserTransport()
	if tr.TLSNextProto == nil {
		t.Fatal("TLSNextProto must be a non-nil empty map — nil would allow HTTP/2 upgrade")
	}
	if len(tr.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto must be empty (HTTP/2 upgrade disabled), got %d entries", len(tr.TLSNextProto))
	}
}

func TestNewBrowserTransport_hasTLSDialer(t *testing.T) {
	tr := newBrowserTransport()
	if tr.DialTLSContext == nil {
		t.Error("DialTLSContext must be set (uTLS Chrome fingerprint dialer)")
	}
}

// ---------------------------------------------------------------------------
// proxyURLFor
// ---------------------------------------------------------------------------

func TestProxyURLFor_noProxy(t *testing.T) {
	cfg := Config{}
	rawURL := "https://example.com/page"
	if got := cfg.proxyURLFor(rawURL); got != rawURL {
		t.Errorf("want %q unchanged, got %q", rawURL, got)
	}
}

func TestProxyURLFor_medium(t *testing.T) {
	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: "https://freedium-mirror.cfd/"},
		},
	}
	rawURL := "https://medium.com/@user/some-article"
	got := cfg.proxyURLFor(rawURL)
	want := "https://freedium-mirror.cfd/" + rawURL
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestProxyURLFor_wwwStripped(t *testing.T) {
	// www.medium.com must match the bare "medium.com" domain key.
	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: "https://freedium-mirror.cfd/"},
		},
	}
	rawURL := "https://www.medium.com/@user/article"
	got := cfg.proxyURLFor(rawURL)
	if !strings.HasPrefix(got, "https://freedium-mirror.cfd/") {
		t.Errorf("www.medium.com should match bare 'medium.com' domain config, got %q", got)
	}
}

func TestProxyURLFor_otherDomain(t *testing.T) {
	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: "https://freedium-mirror.cfd/"},
		},
	}
	rawURL := "https://substack.com/article"
	if got := cfg.proxyURLFor(rawURL); got != rawURL {
		t.Errorf("non-proxied domain must return URL unchanged, got %q", got)
	}
}

func TestProxyURLFor_trailingSlashNormalized(t *testing.T) {
	// proxy_url with or without trailing slash must produce identical results.
	rawURL := "https://medium.com/article"
	withSlash := Config{Domains: map[string]DomainConfig{
		"medium.com": {ProxyURL: "https://freedium-mirror.cfd/"},
	}}
	withoutSlash := Config{Domains: map[string]DomainConfig{
		"medium.com": {ProxyURL: "https://freedium-mirror.cfd"},
	}}
	got1 := withSlash.proxyURLFor(rawURL)
	got2 := withoutSlash.proxyURLFor(rawURL)
	if got1 != got2 {
		t.Errorf("trailing slash on proxy_url should not matter:\n  with slash:    %q\n  without slash: %q", got1, got2)
	}
}

func TestProxyURLFor_invalidURL(t *testing.T) {
	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: "https://freedium-mirror.cfd/"},
		},
	}
	rawURL := "://not-a-valid-url"
	if got := cfg.proxyURLFor(rawURL); got != rawURL {
		t.Errorf("invalid URL must be returned unchanged, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Fetch() — HTTP request integration (httptest server, no real network)
// ---------------------------------------------------------------------------

// requestCapture records details of a single incoming HTTP request.
type requestCapture struct {
	RequestURI string
	UserAgent  string
	SecFetch   string
}

// htmlServer starts an httptest.Server that serves HTML and captures each request.
func htmlServer(t *testing.T, html string) (*httptest.Server, *requestCapture) {
	t.Helper()
	cap := &requestCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.RequestURI = r.RequestURI
		cap.UserAgent = r.Header.Get("User-Agent")
		cap.SecFetch = r.Header.Get("Sec-Fetch-Mode")
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, html)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func TestFetch_sendsBrowserHeaders(t *testing.T) {
	srv, cap := htmlServer(t, "<html><body><h1>Title</h1></body></html>")

	_, err := Fetch(srv.URL+"/page", t.TempDir(), Config{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if !strings.Contains(cap.UserAgent, "Chrome") {
		t.Errorf("User-Agent: want Chrome, got %q", cap.UserAgent)
	}
	if cap.SecFetch != "navigate" {
		t.Errorf("Sec-Fetch-Mode: want %q, got %q", "navigate", cap.SecFetch)
	}
}

func TestFetch_usesProxyURL(t *testing.T) {
	srv, cap := htmlServer(t, "<html><body><h1>Proxied</h1></body></html>")

	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: srv.URL},
		},
	}

	originalURL := "https://medium.com/@user/some-article"
	result, err := Fetch(originalURL, t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// HTTP request must go to the proxy server, not to medium.com.
	// r.RequestURI on the proxy server = "/<originalURL>"
	wantRequestURI := "/" + originalURL
	if cap.RequestURI != wantRequestURI {
		t.Errorf("proxy server received RequestURI %q, want %q", cap.RequestURI, wantRequestURI)
	}

	// result.SourceURL must always be the original URL, not the proxy URL.
	if result.SourceURL != originalURL {
		t.Errorf("result.SourceURL: want %q, got %q", originalURL, result.SourceURL)
	}
}

func TestFetch_cacheKeyUsesOriginalURL(t *testing.T) {
	// The cache file name must be derived from the original URL, not from the
	// proxy URL. This ensures cache lookups always work regardless of proxy config.
	srv, _ := htmlServer(t, "<html><body><h1>Page</h1></body></html>")

	originalURL := "https://medium.com/article-abc"
	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: srv.URL},
		},
	}

	cacheDir := t.TempDir()
	result, err := Fetch(originalURL, cacheDir, cfg)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	expectedKey := cacheKey(originalURL)
	if !strings.Contains(filepath.Base(result.CachePath), expectedKey) {
		t.Errorf("cache path %q must contain key %q (derived from original URL, not proxy URL)",
			result.CachePath, expectedKey)
	}
}

func TestFetch_cacheHitSkipsProxy(t *testing.T) {
	// After a successful fetch, a second call within TTL must use the cached file
	// and not make another HTTP request — even when proxy is configured.
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h1>Cached</h1></body></html>")
	}))
	defer srv.Close()

	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: srv.URL},
		},
	}
	cacheDir := t.TempDir()
	originalURL := "https://medium.com/cached-article"

	if _, err := Fetch(originalURL, cacheDir, cfg); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if _, err := Fetch(originalURL, cacheDir, cfg); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}

	if requestCount != 1 {
		t.Errorf("want exactly 1 HTTP request (second call must use cache), got %d", requestCount)
	}
}

func TestFetch_sourceURLIsOriginalNotProxy(t *testing.T) {
	srv, _ := htmlServer(t, "<html><body><h1>Article</h1></body></html>")
	originalURL := "https://medium.com/test-article"
	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: srv.URL},
		},
	}

	result, err := Fetch(originalURL, t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if strings.Contains(result.SourceURL, "127.0.0.1") {
		t.Errorf("SourceURL must not expose the proxy address, got %q", result.SourceURL)
	}
	if result.SourceURL != originalURL {
		t.Errorf("SourceURL: want %q, got %q", originalURL, result.SourceURL)
	}
}

// ---------------------------------------------------------------------------
// Fragment stripping (#bypass, #section, …)
// ---------------------------------------------------------------------------

func TestFetch_fragmentStrippedFromProxyPath(t *testing.T) {
	// When the user appends #bypass to a medium URL (Freedium convention),
	// the proxy server path must NOT contain the fragment — fragments are
	// client-side only and must never be sent in HTTP requests.
	srv, cap := htmlServer(t, "<html><body><h1>Article</h1></body></html>")
	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: srv.URL},
		},
	}

	urlWithFragment := "https://medium.com/@user/some-article#bypass"
	_, err := Fetch(urlWithFragment, t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if strings.Contains(cap.RequestURI, "#bypass") || strings.Contains(cap.RequestURI, "bypass") {
		t.Errorf("fragment must not appear in the proxy request URI, got %q", cap.RequestURI)
	}
	// The proxy path must contain the medium.com URL (without fragment).
	if !strings.Contains(cap.RequestURI, "medium.com") {
		t.Errorf("proxy request URI must contain the medium.com path, got %q", cap.RequestURI)
	}
}

func TestFetch_fragmentStrippedFromSourceURL(t *testing.T) {
	// result.SourceURL must be the clean URL (no fragment).
	srv, _ := htmlServer(t, "<html><body><h1>Article</h1></body></html>")
	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: srv.URL},
		},
	}

	result, err := Fetch("https://medium.com/@user/article#bypass", t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if strings.Contains(result.SourceURL, "#") {
		t.Errorf("SourceURL must not contain fragment, got %q", result.SourceURL)
	}
}

func TestFetch_fragmentCacheHit(t *testing.T) {
	// A URL with a fragment and the same URL without fragment must resolve to
	// the same cache entry — they are the same resource.
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h1>Page</h1></body></html>")
	}))
	defer srv.Close()

	cfg := Config{
		Domains: map[string]DomainConfig{
			"medium.com": {ProxyURL: srv.URL},
		},
	}
	cacheDir := t.TempDir()

	if _, err := Fetch("https://medium.com/article", cacheDir, cfg); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	// Same article but with #bypass fragment — must be a cache hit, no new request.
	if _, err := Fetch("https://medium.com/article#bypass", cacheDir, cfg); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}

	if requestCount != 1 {
		t.Errorf("URL with and without fragment must share the same cache entry (got %d requests, want 1)", requestCount)
	}
}
