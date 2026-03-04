package catalog_test

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/toolbox-tools/toolbox/internal/catalog"
)

// parseYAML is a test helper that unmarshals catalog YAML without a temp file.
func parseYAML(t *testing.T, raw string) *catalog.Catalog {
	t.Helper()
	var c catalog.Catalog
	if err := yaml.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return &c
}

// ---------------------------------------------------------------------------
// New field parsing
// ---------------------------------------------------------------------------

func TestResourceLimits_Parse(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: test:latest
    fallback: true
    limits:
      cpu: "2"
      memory: "512MB"
      pids: 100
    network: none
    timeout: 30s
`)
	base := cat.Containers["base"]
	if base.Limits.CPU != "2" {
		t.Errorf("Limits.CPU: want %q, got %q", "2", base.Limits.CPU)
	}
	if base.Limits.Memory != "512MB" {
		t.Errorf("Limits.Memory: want %q, got %q", "512MB", base.Limits.Memory)
	}
	if base.Limits.PIDs != 100 {
		t.Errorf("Limits.PIDs: want 100, got %d", base.Limits.PIDs)
	}
	if base.Network != "none" {
		t.Errorf("Network: want %q, got %q", "none", base.Network)
	}
	if base.Timeout != "30s" {
		t.Errorf("Container Timeout: want %q, got %q", "30s", base.Timeout)
	}
}

func TestCatalogGlobalTimeout(t *testing.T) {
	cat := parseYAML(t, `
version: 1
timeout: 2m
containers:
  base:
    image: test:latest
    fallback: true
`)
	if cat.Timeout != "2m" {
		t.Errorf("Catalog.Timeout: want %q, got %q", "2m", cat.Timeout)
	}
}

func TestResourceLimits_ZeroValues(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: test:latest
    fallback: true
`)
	base := cat.Containers["base"]
	if base.Limits.CPU != "" || base.Limits.Memory != "" || base.Limits.PIDs != 0 {
		t.Error("expected zero ResourceLimits when not specified in YAML")
	}
	if base.Network != "" {
		t.Errorf("Network: want empty, got %q", base.Network)
	}
}

// ---------------------------------------------------------------------------
// Routing
// ---------------------------------------------------------------------------

func TestResolve_handles(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
  browser:
    image: browser:latest
    handles: [playwright, chromium]
`)
	cases := []struct {
		cmd  string
		want string
	}{
		{"playwright screenshot https://example.com", "browser"},
		{"chromium --version", "browser"},
		{"python3 script.py", "base"},
		{"node server.js", "base"},
	}
	for _, tc := range cases {
		name, _, err := cat.Resolve(tc.cmd)
		if err != nil {
			t.Errorf("Resolve(%q): unexpected error: %v", tc.cmd, err)
			continue
		}
		if name != tc.want {
			t.Errorf("Resolve(%q): want %q, got %q", tc.cmd, tc.want, name)
		}
	}
}

func TestResolve_fallback(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
`)
	name, _, err := cat.Resolve("anything --at-all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "base" {
		t.Errorf("want fallback %q, got %q", "base", name)
	}
}

func TestResolve_noFallback(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  browser:
    image: browser:latest
    handles: [playwright]
`)
	_, _, err := cat.Resolve("python3 foo.py")
	if err == nil {
		t.Error("expected error when no fallback defined, got nil")
	}
}

func TestResolveByName(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
  data:
    image: data:latest
    handles: [duckdb]
`)
	ct, err := cat.ResolveByName("data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ct.Image != "data:latest" {
		t.Errorf("want image %q, got %q", "data:latest", ct.Image)
	}
}

func TestResolveByName_notFound(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
`)
	_, err := cat.ResolveByName("missing")
	if err == nil {
		t.Error("expected error for missing container, got nil")
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func TestValidate_valid(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
`)
	if errs := cat.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidate_noFallback(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  browser:
    image: browser:latest
    handles: [playwright]
`)
	errs := cat.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation error for missing fallback")
	}
	if !strings.Contains(errs[0], "fallback") {
		t.Errorf("error should mention fallback, got: %q", errs[0])
	}
}

func TestValidate_missingImage(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
  broken:
    handles: [tool]
`)
	errs := cat.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e, "broken") && strings.Contains(e, "image") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about missing image for 'broken', got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// ShellBin
// ---------------------------------------------------------------------------

func TestShellBin_default(t *testing.T) {
	ct := catalog.Container{}
	if ct.ShellBin() != "sh" {
		t.Errorf("want %q, got %q", "sh", ct.ShellBin())
	}
}

func TestShellBin_custom(t *testing.T) {
	ct := catalog.Container{Shell: "bash"}
	if ct.ShellBin() != "bash" {
		t.Errorf("want %q, got %q", "bash", ct.ShellBin())
	}
}

// ---------------------------------------------------------------------------
// Fallback
// ---------------------------------------------------------------------------

func TestFallback_found(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
  other:
    image: other:latest
`)
	name, ct, ok := cat.Fallback()
	if !ok {
		t.Fatal("expected fallback to be found")
	}
	if name != "base" {
		t.Errorf("want fallback name %q, got %q", "base", name)
	}
	if ct.Image != "base:latest" {
		t.Errorf("want fallback image %q, got %q", "base:latest", ct.Image)
	}
}

func TestFallback_notFound(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  browser:
    image: browser:latest
`)
	_, _, ok := cat.Fallback()
	if ok {
		t.Error("expected no fallback to be found")
	}
}

// ---------------------------------------------------------------------------
// Resolve — path-prefix stripping in primary tool extraction
// ---------------------------------------------------------------------------

func TestResolve_pathPrefix(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
  browser:
    image: browser:latest
    handles: [playwright]
`)
	// "/usr/local/bin/playwright" should still route to browser
	name, _, err := cat.Resolve("/usr/local/bin/playwright run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "browser" {
		t.Errorf("want %q, got %q", "browser", name)
	}
}

// ---------------------------------------------------------------------------
// FetchConfig / FetchDomainConfig — proxy_url
// ---------------------------------------------------------------------------

func TestFetchDomainConfig_proxyURL(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
fetch:
  domains:
    medium.com:
      proxy_url: "https://freedium-mirror.cfd/"
      strip_selectors:
        - .paywall
`)
	dc := cat.Fetch.Domains["medium.com"]
	if dc.ProxyURL != "https://freedium-mirror.cfd/" {
		t.Errorf("ProxyURL: want %q, got %q", "https://freedium-mirror.cfd/", dc.ProxyURL)
	}
	if len(dc.StripSelectors) != 1 || dc.StripSelectors[0] != ".paywall" {
		t.Errorf("StripSelectors: want [.paywall], got %v", dc.StripSelectors)
	}
}

func TestFetchDomainConfig_proxyURLEmptyByDefault(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
fetch:
  domains:
    example.com:
      strip_selectors:
        - nav
`)
	dc := cat.Fetch.Domains["example.com"]
	if dc.ProxyURL != "" {
		t.Errorf("ProxyURL: want empty when not set, got %q", dc.ProxyURL)
	}
}

func TestFetchConfig_multipleDomains(t *testing.T) {
	// Verify that multiple domain entries coexist and are parsed independently.
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
fetch:
  domains:
    medium.com:
      proxy_url: "https://freedium-mirror.cfd/"
    digi24.ro:
      strip_selectors:
        - .paywalled-content
`)
	if got := cat.Fetch.Domains["medium.com"].ProxyURL; got != "https://freedium-mirror.cfd/" {
		t.Errorf("medium.com ProxyURL: want freedium, got %q", got)
	}
	if got := cat.Fetch.Domains["digi24.ro"].ProxyURL; got != "" {
		t.Errorf("digi24.ro ProxyURL: want empty, got %q", got)
	}
	if sels := cat.Fetch.Domains["digi24.ro"].StripSelectors; len(sels) != 1 {
		t.Errorf("digi24.ro StripSelectors: want 1 entry, got %v", sels)
	}
}

func TestFetchConfig_cacheTTLAndAllowedDomains(t *testing.T) {
	cat := parseYAML(t, `
version: 1
containers:
  base:
    image: base:latest
    fallback: true
fetch:
  cache_ttl: 48h
  allowed_domains:
    - "*.github.com"
    - "docs.example.com"
`)
	if cat.Fetch.CacheTTL != "48h" {
		t.Errorf("CacheTTL: want %q, got %q", "48h", cat.Fetch.CacheTTL)
	}
	if len(cat.Fetch.AllowedDomains) != 2 {
		t.Errorf("AllowedDomains: want 2 entries, got %v", cat.Fetch.AllowedDomains)
	}
}
