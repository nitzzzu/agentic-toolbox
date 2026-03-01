package env

// White-box tests — package env, so unexported functions are accessible.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/toolbox-tools/toolbox/internal/catalog"
)

// ---------------------------------------------------------------------------
// globMatch
// ---------------------------------------------------------------------------

func TestGlobMatch_exact(t *testing.T) {
	cases := []struct {
		pattern, key string
		want         bool
	}{
		{"CI", "CI", true},
		{"CI", "ci", false},
		{"CI", "CIX", false},
		{"DEBUG", "DEBUG", true},
		{"DEBUG", "DEBUGX", false},
	}
	for _, tc := range cases {
		got := globMatch(tc.pattern, tc.key)
		if got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.key, got, tc.want)
		}
	}
}

func TestGlobMatch_prefix(t *testing.T) {
	cases := []struct {
		pattern, key string
		want         bool
	}{
		{"AWS_*", "AWS_ACCESS_KEY_ID", true},
		{"AWS_*", "AWS_SECRET", true},
		{"AWS_*", "AWS_", true},
		{"AWS_*", "NOTAWS_FOO", false},
		{"OPENAI_*", "OPENAI_API_KEY", true},
		{"OPENAI_*", "OPENAI_ORG_ID", true},
		{"OPENAI_*", "openai_api_key", false}, // case-sensitive
	}
	for _, tc := range cases {
		got := globMatch(tc.pattern, tc.key)
		if got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.key, got, tc.want)
		}
	}
}

func TestGlobMatch_suffix(t *testing.T) {
	got := globMatch("*_KEY", "MY_SECRET_KEY")
	if !got {
		t.Error("expected *_KEY to match MY_SECRET_KEY")
	}
	got = globMatch("*_KEY", "MY_SECRET_KEYS")
	if got {
		t.Error("expected *_KEY not to match MY_SECRET_KEYS")
	}
}

func TestGlobMatch_prefixSuffix(t *testing.T) {
	got := globMatch("AWS_*_ID", "AWS_ACCESS_KEY_ID")
	if !got {
		t.Error("expected AWS_*_ID to match AWS_ACCESS_KEY_ID")
	}
}

// ---------------------------------------------------------------------------
// stripQuotes
// ---------------------------------------------------------------------------

func TestStripQuotes(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`"hello"`, "hello"},
		{`'world'`, "world"},
		{`"no-end`, `"no-end`},
		{`plain`, `plain`},
		{`""`, ``},
		{`"a"`, `a`},
	}
	for _, tc := range cases {
		got := stripQuotes(tc.in)
		if got != tc.want {
			t.Errorf("stripQuotes(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ParseEnvFile
// ---------------------------------------------------------------------------

func TestParseEnvFile_basic(t *testing.T) {
	path := writeTempEnv(t, "KEY1=value1\nKEY2=value2\n")
	vars, err := ParseEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if vars["KEY1"] != "value1" {
		t.Errorf("KEY1: want %q, got %q", "value1", vars["KEY1"])
	}
	if vars["KEY2"] != "value2" {
		t.Errorf("KEY2: want %q, got %q", "value2", vars["KEY2"])
	}
}

func TestParseEnvFile_comments(t *testing.T) {
	path := writeTempEnv(t, "# This is a comment\nKEY=val\n\n# another\n")
	vars, err := ParseEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 1 {
		t.Errorf("expected 1 var, got %d: %v", len(vars), vars)
	}
	if vars["KEY"] != "val" {
		t.Errorf("KEY: want %q, got %q", "val", vars["KEY"])
	}
}

func TestParseEnvFile_quotedValues(t *testing.T) {
	path := writeTempEnv(t, `KEY1="quoted value"
KEY2='single quoted'
`)
	vars, err := ParseEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if vars["KEY1"] != "quoted value" {
		t.Errorf("KEY1: want %q, got %q", "quoted value", vars["KEY1"])
	}
	if vars["KEY2"] != "single quoted" {
		t.Errorf("KEY2: want %q, got %q", "single quoted", vars["KEY2"])
	}
}

func TestParseEnvFile_missing(t *testing.T) {
	vars, err := ParseEnvFile("/nonexistent/path/env")
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 0 {
		t.Errorf("expected empty map for missing file, got %v", vars)
	}
}

// ---------------------------------------------------------------------------
// UpdateEnvFile / DeleteFromEnvFile
// ---------------------------------------------------------------------------

func TestUpdateEnvFile_merge(t *testing.T) {
	path := writeTempEnv(t, "EXISTING=old\nOTHER=keep\n")

	if err := UpdateEnvFile(path, map[string]string{"EXISTING": "new", "ADDED": "yes"}); err != nil {
		t.Fatal(err)
	}

	vars, err := ParseEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if vars["EXISTING"] != "new" {
		t.Errorf("EXISTING: want %q, got %q", "new", vars["EXISTING"])
	}
	if vars["OTHER"] != "keep" {
		t.Errorf("OTHER: want %q, got %q", "keep", vars["OTHER"])
	}
	if vars["ADDED"] != "yes" {
		t.Errorf("ADDED: want %q, got %q", "yes", vars["ADDED"])
	}
}

func TestDeleteFromEnvFile(t *testing.T) {
	path := writeTempEnv(t, "A=1\nB=2\nC=3\n")

	if err := DeleteFromEnvFile(path, "B"); err != nil {
		t.Fatal(err)
	}

	vars, err := ParseEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vars["B"]; ok {
		t.Error("B should have been deleted")
	}
	if vars["A"] != "1" || vars["C"] != "3" {
		t.Errorf("A and C should remain, got %v", vars)
	}
}

// ---------------------------------------------------------------------------
// Merged — forward/deny rules
// ---------------------------------------------------------------------------

func TestMerged_forwardDeny(t *testing.T) {
	root := t.TempDir()
	dotDir := filepath.Join(root, ".toolbox")
	if err := os.MkdirAll(dotDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create empty env files so Merged doesn't error.
	os.WriteFile(filepath.Join(dotDir, "env"), []byte(""), 0644)

	// Set a known env var that should be forwarded.
	t.Setenv("TOOLBOX_TEST_VAR", "hello")
	t.Setenv("TOOLBOX_TEST_SECRET_KEY", "secret")
	t.Setenv("HOME", "shouldnotforward")

	rules := catalog.EnvRules{
		Forward: []string{"TOOLBOX_TEST_*"},
		Deny:    []string{"HOME"},
	}

	vars, err := Merged(root, rules, nil)
	if err != nil {
		t.Fatalf("Merged: %v", err)
	}

	merged := sliceToMap(vars)
	if merged["TOOLBOX_TEST_VAR"] != "hello" {
		t.Error("TOOLBOX_TEST_VAR should be forwarded")
	}
	if _, ok := merged["HOME"]; ok {
		t.Error("HOME should be denied")
	}
}

func TestMerged_containerEnvOverrides(t *testing.T) {
	root := t.TempDir()
	dotDir := filepath.Join(root, ".toolbox")
	os.MkdirAll(dotDir, 0755)
	os.WriteFile(filepath.Join(dotDir, "env"), []byte("MY_VAR=from-workspace-env\n"), 0644)

	rules := catalog.EnvRules{}
	containerEnv := map[string]string{"MY_VAR": "from-container-catalog"}

	vars, err := Merged(root, rules, containerEnv)
	if err != nil {
		t.Fatalf("Merged: %v", err)
	}

	merged := sliceToMap(vars)
	if merged["MY_VAR"] != "from-container-catalog" {
		t.Errorf("container env should win, got %q", merged["MY_VAR"])
	}
}

func TestMerged_workspaceEnvOverridesHost(t *testing.T) {
	root := t.TempDir()
	dotDir := filepath.Join(root, ".toolbox")
	os.MkdirAll(dotDir, 0755)
	os.WriteFile(filepath.Join(dotDir, "env"), []byte("OVERRIDE_ME=workspace-value\n"), 0644)

	t.Setenv("OVERRIDE_ME", "host-value")

	rules := catalog.EnvRules{
		Forward: []string{"OVERRIDE_ME"},
	}

	vars, err := Merged(root, rules, nil)
	if err != nil {
		t.Fatalf("Merged: %v", err)
	}

	merged := sliceToMap(vars)
	if merged["OVERRIDE_ME"] != "workspace-value" {
		t.Errorf("workspace env should override host, got %q", merged["OVERRIDE_ME"])
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeTempEnv(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "env*")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Close()
	return f.Name()
}

func sliceToMap(kvs []string) map[string]string {
	m := make(map[string]string)
	for _, kv := range kvs {
		for i, ch := range kv {
			if ch == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return m
}
