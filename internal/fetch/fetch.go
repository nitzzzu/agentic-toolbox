package fetch

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

const defaultTTL = 24 * time.Hour

// ---------------------------------------------------------------------------
// Browser-like HTTP client (uTLS + Chrome headers + cookie jar)
// ---------------------------------------------------------------------------

var (
	browserOnce   sync.Once
	browserClient *http.Client
)

// newBrowserTransport returns an http.Transport whose TLS connections use the
// uTLS Chrome_Auto fingerprint instead of Go's default TLS stack, making the
// client's TLS ClientHello indistinguishable from a real Chrome browser.
//
// ALPN is limited to ["http/1.1"] to keep the protocol layer consistent:
// Chrome's default ClientHello advertises ["h2", "http/1.1"], so when a server
// selects h2 via ALPN but the client then speaks HTTP/1.1, the server sends a
// GOAWAY or RST and the connection fails with EOF.  By advertising only
// "http/1.1" the server can never negotiate h2, and HTTP/1.1 is used
// throughout.  utls.Config.NextProtos overrides the ALPN extension in the
// fingerprint spec, so the rest of the Chrome ClientHello is unchanged.
func newBrowserTransport() *http.Transport {
	return &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialConn, err := (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			host, _, _ := net.SplitHostPort(addr)
			uConn := utls.UClient(dialConn, &utls.Config{
				ServerName: host,
				// Limit ALPN to http/1.1 so the server cannot select h2 via
				// ALPN and cause an HTTP/2 vs HTTP/1.1 protocol mismatch.
				NextProtos: []string{"http/1.1"},
			}, utls.HelloChrome_Auto)
			if err := uConn.HandshakeContext(ctx); err != nil {
				dialConn.Close()
				return nil, fmt.Errorf("TLS handshake: %w", err)
			}
			return uConn, nil
		},
		// Belt-and-suspenders: even if ALPN somehow negotiates h2, do not
		// upgrade — Go's h2 transport does not work over a *utls.UConn.
		TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}
}

func getBrowserClient() *http.Client {
	browserOnce.Do(func() {
		jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		browserClient = &http.Client{
			Jar:       jar,
			Transport: newBrowserTransport(),
			Timeout:   30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return http.ErrUseLastResponse
				}
				setBrowserHeaders(req)
				return nil
			},
		}
	})
	return browserClient
}

// setBrowserHeaders sets Chrome 132 / Windows request headers.
// Accept-Encoding is intentionally omitted — Go's Transport adds gzip
// automatically and handles decompression transparently.
func setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-CH-UA", `"Not A(Brand";v="99", "Google Chrome";v="132", "Chromium";v="132"`)
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Cache-Control", "max-age=0")
}

// Config holds settings for fetch operations.
type Config struct {
	CacheTTL       time.Duration
	StripSelectors []string            // global: applied to every URL
	AllowedDomains []string
	Domains        map[string]DomainConfig // per-domain additions, keyed by hostname (e.g. "digi24.ro")
}

// DomainConfig holds per-domain fetch settings.
type DomainConfig struct {
	StripSelectors []string // merged with Config.StripSelectors for this domain
	ProxyURL       string   // URL prefix prepended before the actual URL when fetching (e.g. "https://freedium-mirror.cfd/")
}

// selectorsForURL returns global strip selectors merged with any domain-specific ones.
// Domain lookup tries the full hostname, then strips a leading "www." prefix.
func (cfg Config) selectorsForURL(rawURL string) []string {
	selectors := make([]string, len(cfg.StripSelectors))
	copy(selectors, cfg.StripSelectors)

	u, err := url.Parse(rawURL)
	if err != nil {
		return selectors
	}
	host := strings.ToLower(u.Hostname())
	bare := strings.TrimPrefix(host, "www.")

	for _, key := range []string{host, bare} {
		if dc, ok := cfg.Domains[key]; ok {
			selectors = append(selectors, dc.StripSelectors...)
			break
		}
	}
	return selectors
}

// proxyURLFor returns the URL that should actually be fetched.
// If the domain has a proxy_url configured, it returns proxyURL+rawURL;
// otherwise it returns rawURL unchanged.
// The cache key and domain validation always use the original rawURL.
func (cfg Config) proxyURLFor(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := strings.ToLower(u.Hostname())
	bare := strings.TrimPrefix(host, "www.")

	for _, key := range []string{host, bare} {
		if dc, ok := cfg.Domains[key]; ok && dc.ProxyURL != "" {
			return strings.TrimRight(dc.ProxyURL, "/") + "/" + rawURL
		}
	}
	return rawURL
}

// TOCEntry is a single heading in the table of contents.
type TOCEntry struct {
	Level     int
	Title     string
	StartLine int
	EndLine   int
}

// Result is the structured output of a fetch or read operation.
type Result struct {
	SourceURL  string
	Type       string         // "html→markdown", "markdown", "local-file"
	CachePath  string         // path to cached/source file
	Lines      int
	Generated  time.Time
	TOC        []TOCEntry
	CodeBlocks map[string]int // language → count
	Symbols    []string
}

type cacheMeta struct {
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetched_at"`
	TTL       string    `json:"ttl"`
	Type      string    `json:"type"`
}

// Fetch downloads a URL, converts to markdown, caches it, and returns a summary.
// cacheDir is the directory where .md and .meta.json files are written.
func Fetch(rawURL, cacheDir string, cfg Config) (*Result, error) {
	// Normalize: strip URL fragment before any processing.
	// Fragments (#bypass, #section, …) are client-side only and must never
	// appear in proxy URL construction or cache keys.  Users sometimes copy
	// Freedium-style URLs with #bypass appended; stripping here ensures
	// consistent cache hits and clean proxy paths.
	if u, err := url.Parse(rawURL); err == nil && u.Fragment != "" {
		u.Fragment = ""
		rawURL = u.String()
	}

	if err := validateDomain(rawURL, cfg.AllowedDomains); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	key := cacheKey(rawURL)
	mdPath := filepath.Join(cacheDir, key+".md")
	metaPath := filepath.Join(cacheDir, key+".meta.json")

	ttl := cfg.CacheTTL
	if ttl == 0 {
		ttl = defaultTTL
	}

	// Return cached result if still fresh.
	if meta, err := readMeta(metaPath); err == nil {
		if time.Since(meta.FetchedAt) < ttl {
			return buildResult(rawURL, mdPath, meta.Type)
		}
	}

	// Download with browser-like headers and uTLS fingerprint.
	// The proxy URL (if any) is used for the actual HTTP request;
	// rawURL remains the cache key and source identifier.
	fetchURL := cfg.proxyURLFor(rawURL)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	setBrowserHeaders(req)
	resp, err := getBrowserClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	docType := detectContentType(rawURL, contentType)

	var markdown string
	switch docType {
	case "html→markdown":
		markdown, err = htmlToMarkdown(string(body), rawURL, cfg.selectorsForURL(rawURL))
		if err != nil {
			return nil, fmt.Errorf("convert HTML: %w", err)
		}
	default:
		markdown = string(body)
		docType = "markdown"
	}

	if err := os.WriteFile(mdPath, []byte(markdown), 0644); err != nil {
		return nil, fmt.Errorf("write cache: %w", err)
	}

	meta := cacheMeta{
		URL:       rawURL,
		FetchedAt: time.Now(),
		TTL:       ttl.String(),
		Type:      docType,
	}
	if metaBytes, err := json.Marshal(meta); err == nil {
		_ = os.WriteFile(metaPath, metaBytes, 0644)
	}

	return buildResult(rawURL, mdPath, docType)
}

// FetchLines reads a line range from the cached markdown for a URL.
// Returns numbered lines: "  42  line content\n"
func FetchLines(rawURL, cacheDir string, start, end int) (string, error) {
	key := cacheKey(rawURL)
	mdPath := filepath.Join(cacheDir, key+".md")
	return readLines(mdPath, start, end, fmt.Sprintf("cache miss — run `toolbox fetch %s` first", rawURL))
}

// Read processes a local file through the same analysis pipeline (no download, no cache).
func Read(filePath string) (*Result, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filePath, err)
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	toc := extractTOC(lines)
	assignTOCRanges(toc, len(lines))

	return &Result{
		SourceURL:  filePath,
		Type:       "local-file",
		CachePath:  filePath,
		Lines:      len(lines),
		Generated:  time.Now(),
		TOC:        toc,
		CodeBlocks: countCodeBlocks(content),
		Symbols:    extractSymbols(content),
	}, nil
}

// ReadLines returns a numbered line range from a local file.
func ReadLines(filePath string, start, end int) (string, error) {
	return readLines(filePath, start, end, fmt.Sprintf("cannot read file %s", filePath))
}

// Format renders a Result as the structured summary shown to agents.
func Format(r *Result) string {
	var sb strings.Builder

	if r.Type == "local-file" {
		fmt.Fprintf(&sb, "%-10s %s\n", "file:", r.SourceURL)
	} else {
		fmt.Fprintf(&sb, "%-10s %s\n", "source:", r.SourceURL)
	}
	fmt.Fprintf(&sb, "%-10s %s\n", "type:", r.Type)
	if r.Type != "local-file" {
		fmt.Fprintf(&sb, "%-10s %s\n", "cached:", r.CachePath)
	}
	fmt.Fprintf(&sb, "%-10s %d\n", "lines:", r.Lines)
	fmt.Fprintf(&sb, "%-10s %s\n", "generated:", r.Generated.UTC().Format(time.RFC3339))

	if len(r.TOC) > 0 {
		sb.WriteString("\ntoc:\n")
		for i, e := range r.TOC {
			lineRef := fmt.Sprintf("lines %d\u2013%d", e.StartLine, e.EndLine)
			fmt.Fprintf(&sb, "  %d. %-30s %s\n", i+1, e.Title, lineRef)
		}
	}

	if len(r.CodeBlocks) > 0 {
		total := 0
		var langs []string
		for lang, count := range r.CodeBlocks {
			total += count
			langs = append(langs, fmt.Sprintf("%s: %d", lang, count))
		}
		sort.Strings(langs)
		fmt.Fprintf(&sb, "\ncode_blocks: %d  (%s)\n", total, strings.Join(langs, ", "))
	}

	if len(r.Symbols) > 0 {
		fmt.Fprintf(&sb, "symbols:     [%s]\n", strings.Join(r.Symbols, ", "))
	}

	// Hint: show the --lines command for the middle TOC section.
	if len(r.TOC) > 0 {
		best := r.TOC[len(r.TOC)/2]
		if r.Type == "local-file" {
			fmt.Fprintf(&sb, "\n→ toolbox read --lines %d-%d %s\n", best.StartLine, best.EndLine, r.SourceURL)
		} else {
			fmt.Fprintf(&sb, "\n→ toolbox fetch --lines %d-%d %s\n", best.StartLine, best.EndLine, r.SourceURL)
		}
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// cacheKey returns a 16-hex-char SHA256 of the normalized URL (query stripped).
func cacheKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil {
		u.RawQuery = ""
		u.Fragment = ""
		rawURL = u.String()
	}
	sum := sha256.Sum256([]byte(rawURL))
	return fmt.Sprintf("%x", sum[:8]) // 16 hex chars
}

func readMeta(path string) (*cacheMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m cacheMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func buildResult(rawURL, mdPath, docType string) (*Result, error) {
	data, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, fmt.Errorf("read cached file: %w", err)
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	toc := extractTOC(lines)
	assignTOCRanges(toc, len(lines))

	return &Result{
		SourceURL:  rawURL,
		Type:       docType,
		CachePath:  mdPath,
		Lines:      len(lines),
		Generated:  time.Now(),
		TOC:        toc,
		CodeBlocks: countCodeBlocks(content),
		Symbols:    extractSymbols(content),
	}, nil
}

func detectContentType(rawURL, contentType string) string {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "text/html") {
		return "html→markdown"
	}
	if strings.Contains(ct, "text/markdown") || strings.Contains(ct, "text/x-markdown") {
		return "markdown"
	}

	// Inspect URL path extension.
	u, err := url.Parse(rawURL)
	if err == nil {
		ext := strings.ToLower(filepath.Ext(u.Path))
		if ext == ".md" || ext == ".markdown" || ext == ".txt" {
			return "markdown"
		}
	}

	// Default: assume HTML and convert.
	return "html→markdown"
}

// htmlToMarkdown converts raw HTML to clean markdown using html-to-markdown/v2.
//
// stripSelectors supports three forms:
//   - Tag names:        "nav", "footer", "aside"  → removed via TagTypeRemove
//   - Class selectors:  ".sidebar", ".cookie-bar" → removed via DOM pre-render walk
//   - ID selectors:     "#cookie-banner", "#nav"  → removed via DOM pre-render walk
func htmlToMarkdown(rawHTML, sourceURL string, stripSelectors []string) (string, error) {
	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(),
		),
	)

	var classes, ids []string
	for _, sel := range stripSelectors {
		switch {
		case strings.HasPrefix(sel, "."):
			classes = append(classes, sel[1:])
		case strings.HasPrefix(sel, "#"):
			ids = append(ids, sel[1:])
		default:
			conv.Register.TagType(sel, converter.TagTypeRemove, converter.PriorityStandard)
		}
	}

	// Register a pre-render pass to remove nodes by class or ID.
	if len(classes) > 0 || len(ids) > 0 {
		conv.Register.PreRenderer(domRemoveBySelector(classes, ids), converter.PriorityEarly)
	}

	return conv.ConvertString(rawHTML, converter.WithDomain(sourceURL))
}

// domRemoveBySelector returns a PreRenderer that walks the HTML node tree and
// removes any element whose class list intersects classes, or whose id matches ids.
func domRemoveBySelector(classes, ids []string) converter.HandlePreRenderFunc {
	return func(_ converter.Context, doc *html.Node) {
		var toRemove []*html.Node
		walkNodes(doc, func(n *html.Node) {
			if n.Type != html.ElementNode {
				return
			}
			for _, attr := range n.Attr {
				switch attr.Key {
				case "class":
					for _, nc := range strings.Fields(attr.Val) {
						for _, wc := range classes {
							if nc == wc {
								toRemove = append(toRemove, n)
								return
							}
						}
					}
				case "id":
					for _, wid := range ids {
						if attr.Val == wid {
							toRemove = append(toRemove, n)
							return
						}
					}
				}
			}
		})
		for _, n := range toRemove {
			if n.Parent != nil {
				n.Parent.RemoveChild(n)
			}
		}
	}
}

// walkNodes calls fn on every node in the subtree rooted at n.
// Collects nodes first to avoid iterator invalidation during removal.
func walkNodes(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNodes(c, fn)
	}
}

var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.+)`)

func extractTOC(lines []string) []TOCEntry {
	var toc []TOCEntry
	for i, line := range lines {
		m := headingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		toc = append(toc, TOCEntry{
			Level:     len(m[1]),
			Title:     strings.TrimSpace(m[2]),
			StartLine: i + 1, // 1-indexed
		})
	}
	return toc
}

func assignTOCRanges(toc []TOCEntry, totalLines int) {
	for i := range toc {
		if i+1 < len(toc) {
			toc[i].EndLine = toc[i+1].StartLine - 1
		} else {
			toc[i].EndLine = totalLines
		}
	}
}

// symbolRe matches backtick-wrapped identifiers like `useEffect` or `AbortController`.
var symbolRe = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]{1,50})`")

func extractSymbols(content string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, m := range symbolRe.FindAllStringSubmatch(content, -1) {
		sym := m[1]
		if !seen[sym] {
			seen[sym] = true
			result = append(result, sym)
		}
		if len(result) >= 20 {
			break
		}
	}
	return result
}

// codeBlockRe matches fenced code block opening lines (``` or ```language).
var codeBlockRe = regexp.MustCompile("(?m)^```([a-zA-Z0-9_+.-]*)")

func countCodeBlocks(content string) map[string]int {
	counts := make(map[string]int)
	for _, m := range codeBlockRe.FindAllStringSubmatch(content, -1) {
		lang := strings.ToLower(m[1])
		if lang == "" {
			lang = "unknown"
		}
		counts[lang]++
	}
	return counts
}

func validateDomain(rawURL string, allowedDomains []string) error {
	if len(allowedDomains) == 0 {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	for _, pattern := range allowedDomains {
		if matchDomain(strings.ToLower(pattern), host) {
			return nil
		}
	}
	return fmt.Errorf("domain %q not in fetch.allowed_domains list", host)
}

func matchDomain(pattern, host string) bool {
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".github.com"
		return host == pattern[2:] || strings.HasSuffix(host, suffix)
	}
	return host == pattern
}

// readLines reads a 1-indexed range from a file and returns numbered output.
func readLines(filePath string, start, end int, missingMsg string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("%s", missingMsg)
	}

	lines := strings.Split(string(data), "\n")
	total := len(lines)

	if start < 1 {
		start = 1
	}
	if end < 1 || end > total {
		end = total
	}
	if start > end {
		return "", fmt.Errorf("invalid range %d-%d (file has %d lines)", start, end, total)
	}

	var sb strings.Builder
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&sb, "%4d  %s\n", i+1, lines[i])
	}
	return sb.String(), nil
}
