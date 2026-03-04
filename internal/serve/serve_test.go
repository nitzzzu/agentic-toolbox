package serve_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolbox-tools/toolbox/internal/catalog"
	"github.com/toolbox-tools/toolbox/internal/container"
	"github.com/toolbox-tools/toolbox/internal/fetch"
	"github.com/toolbox-tools/toolbox/internal/serve"
)

// ---------------------------------------------------------------------------
// Mock runtime
// ---------------------------------------------------------------------------

type mockRuntime struct {
	execCode int
	execErr  error
}

func (m *mockRuntime) Name() string                                              { return "mock" }
func (m *mockRuntime) Run(opts container.RunOpts) error                          { return nil }
func (m *mockRuntime) Stop(name string) error                                    { return nil }
func (m *mockRuntime) Remove(name string, _ bool) error                          { return nil }
func (m *mockRuntime) Pull(image string) error                                   { return nil }
func (m *mockRuntime) IsRunning(name string) (bool, error)                       { return true, nil }
func (m *mockRuntime) RunEphemeral(opts container.EphemeralOpts) (int, error)    { return 0, nil }
func (m *mockRuntime) Exec(opts container.ExecOpts) (int, error) {
	return m.execCode, m.execErr
}
func (m *mockRuntime) Status(prefix string) ([]container.ContainerStatus, error) {
	return []container.ContainerStatus{
		{Name: "toolbox-test-base", Image: "base:latest", Status: "Up", Created: "1h ago"},
	}, nil
}

// ---------------------------------------------------------------------------
// Test helper
// ---------------------------------------------------------------------------

func newTestHandler(t *testing.T, rt *mockRuntime) http.Handler {
	t.Helper()
	root := t.TempDir()
	dotDir := filepath.Join(root, ".toolbox")
	if err := os.MkdirAll(dotDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dotDir, "env"), []byte(""), 0644)

	cat := &catalog.Catalog{
		Containers: map[string]catalog.Container{
			"base": {Image: "base:latest", Fallback: true},
		},
	}
	mgr := &container.Manager{
		Runtime:       rt,
		WorkspaceRoot: root,
		Catalog:       cat,
	}
	return serve.NewHandler(mgr, fetch.Config{})
}

// ---------------------------------------------------------------------------
// GET /health
// ---------------------------------------------------------------------------

func TestHealth_ok(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("want JSON content-type, got %q", ct)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("want ok=true, got %v", resp["ok"])
	}
	if resp["runtime"] != "mock" {
		t.Errorf("want runtime=mock, got %v", resp["runtime"])
	}
}

func TestHealth_wrongMethod(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /status
// ---------------------------------------------------------------------------

func TestStatus_ok(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}

	var resp []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, w.Body.String())
	}
	if len(resp) == 0 {
		t.Fatal("expected at least one container status")
	}
	if resp[0]["Name"] != "toolbox-test-base" {
		t.Errorf("unexpected container name: %v", resp[0]["Name"])
	}
}

func TestStatus_wrongMethod(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodPost, "/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /exec
// ---------------------------------------------------------------------------

func TestExec_success(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{execCode: 0})
	body := `{"cmd":"echo hello"}`
	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d — body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["exit"].(float64) != 0 {
		t.Errorf("want exit=0, got %v", resp["exit"])
	}
}

func TestExec_nonZeroExit(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{execCode: 1})
	body := `{"cmd":"false"}`
	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200 (non-zero exit is not a server error), got %d", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["exit"].(float64) != 1 {
		t.Errorf("want exit=1, got %v", resp["exit"])
	}
}

func TestExec_missingCmd(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	body := `{"container":"base"}`
	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing cmd, got %d", w.Code)
	}
}

func TestExec_invalidJSON(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader("not-json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid JSON, got %d", w.Code)
	}
}

func TestExec_wrongMethod(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

func TestExec_responseHasMsField(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	body := `{"cmd":"echo"}`
	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["ms"]; !ok {
		t.Error("response should include ms (elapsed milliseconds)")
	}
}

// ---------------------------------------------------------------------------
// GET /workspace — directory listing
// ---------------------------------------------------------------------------

func TestWorkspaceList_ok(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})

	// Write a file into the temp workspace so there's something to list.
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/workspace", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	entries, ok := resp["entries"].([]interface{})
	if !ok {
		t.Fatal("expected entries array")
	}
	found := false
	for _, e := range entries {
		em := e.(map[string]interface{})
		if em["name"] == "hello.txt" {
			found = true
		}
	}
	if !found {
		t.Error("hello.txt not found in listing")
	}
}

func TestWorkspaceList_subPath(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.MkdirAll(filepath.Join(root, "sub"), 0755)
	os.WriteFile(filepath.Join(root, "sub", "a.csv"), []byte("data"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/workspace?path=sub", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	entries := resp["entries"].([]interface{})
	if len(entries) == 0 {
		t.Fatal("expected at least one entry in sub/")
	}
	em := entries[0].(map[string]interface{})
	if em["name"] != "a.csv" {
		t.Errorf("expected a.csv, got %v", em["name"])
	}
}

func TestWorkspaceList_wrongMethod(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodPost, "/workspace", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

func TestWorkspaceList_notFound(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodGet, "/workspace?path=doesnotexist", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// /workspace/{path} — file CRUD
// ---------------------------------------------------------------------------

func TestWorkspaceFile_putAndGet(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})

	// PUT
	putReq := httptest.NewRequest(http.MethodPut, "/workspace/data.txt", strings.NewReader("hello world"))
	putW := httptest.NewRecorder()
	h.ServeHTTP(putW, putReq)
	if putW.Code != http.StatusNoContent {
		t.Fatalf("PUT want 204, got %d — %s", putW.Code, putW.Body.String())
	}

	// GET
	getReq := httptest.NewRequest(http.MethodGet, "/workspace/data.txt", nil)
	getW := httptest.NewRecorder()
	h.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d", getW.Code)
	}
	if got := getW.Body.String(); got != "hello world" {
		t.Errorf("want 'hello world', got %q", got)
	}
}

func TestWorkspaceFile_putCreatesParentDirs(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})

	req := httptest.NewRequest(http.MethodPut, "/workspace/a/b/c/nested.json", strings.NewReader(`{"ok":true}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d — %s", w.Code, w.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/workspace/a/b/c/nested.json", nil)
	getW := httptest.NewRecorder()
	h.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("want 200 after nested PUT, got %d", getW.Code)
	}
}

func TestWorkspaceFile_getNotFound(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodGet, "/workspace/missing.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestWorkspaceFile_delete(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "todelete.txt"), []byte("bye"), 0644)

	req := httptest.NewRequest(http.MethodDelete, "/workspace/todelete.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE want 204, got %d", w.Code)
	}

	// Verify gone.
	getReq := httptest.NewRequest(http.MethodGet, "/workspace/todelete.txt", nil)
	getW := httptest.NewRecorder()
	h.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusNotFound {
		t.Errorf("want 404 after delete, got %d", getW.Code)
	}
}

func TestWorkspaceFile_deleteNotFound(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodDelete, "/workspace/ghost.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestWorkspaceFile_pathTraversal(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodGet, "/workspace/../../etc/passwd", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Error("path traversal must not succeed")
	}
}

func TestWorkspaceFile_wrongMethod(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodPost, "/workspace/file.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /workspace/{path}?offset=N&limit=N — line-ranged reads
// ---------------------------------------------------------------------------

func TestWorkspaceRead_withOffset(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "lines.txt"), []byte("a\nb\nc\nd\ne"), 0644)

	// Read from line 3 (1-indexed) → should get c, d, e
	req := httptest.NewRequest(http.MethodGet, "/workspace/lines.txt?offset=3", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "c\nd\ne" {
		t.Errorf("want 'c\\nd\\ne', got %q", got)
	}
}

func TestWorkspaceRead_withLimit(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "lines.txt"), []byte("a\nb\nc\nd\ne"), 0644)

	// Read first 2 lines
	req := httptest.NewRequest(http.MethodGet, "/workspace/lines.txt?limit=2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "a\nb" {
		t.Errorf("want 'a\\nb', got %q", got)
	}
}

func TestWorkspaceRead_withOffsetAndLimit(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "lines.txt"), []byte("a\nb\nc\nd\ne"), 0644)

	// Lines 2-3 (offset=2, limit=2) → b, c
	req := httptest.NewRequest(http.MethodGet, "/workspace/lines.txt?offset=2&limit=2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "b\nc" {
		t.Errorf("want 'b\\nc', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// GET /find — glob file search
// ---------------------------------------------------------------------------

func TestFind_basicGlob(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "foo.ts"), []byte(""), 0644)
	os.WriteFile(filepath.Join(root, "bar.ts"), []byte(""), 0644)
	os.WriteFile(filepath.Join(root, "baz.py"), []byte(""), 0644)

	req := httptest.NewRequest(http.MethodGet, "/find?pattern=*.ts", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "foo.ts") || !strings.Contains(body, "bar.ts") {
		t.Errorf("expected .ts files in results, got: %s", body)
	}
	if strings.Contains(body, "baz.py") {
		t.Errorf("baz.py should not match *.ts")
	}
}

func TestFind_doubleStarGlob(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.MkdirAll(filepath.Join(root, "src", "deep"), 0755)
	os.WriteFile(filepath.Join(root, "src", "deep", "nested.ts"), []byte(""), 0644)
	os.WriteFile(filepath.Join(root, "src", "top.ts"), []byte(""), 0644)

	req := httptest.NewRequest(http.MethodGet, "/find?pattern=**/*.ts", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "nested.ts") || !strings.Contains(body, "top.ts") {
		t.Errorf("expected both nested and top-level .ts files, got: %s", body)
	}
}

func TestFind_withSubPath(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.MkdirAll(filepath.Join(root, "src"), 0755)
	os.MkdirAll(filepath.Join(root, "other"), 0755)
	os.WriteFile(filepath.Join(root, "src", "a.ts"), []byte(""), 0644)
	os.WriteFile(filepath.Join(root, "other", "b.ts"), []byte(""), 0644)

	req := httptest.NewRequest(http.MethodGet, "/find?pattern=*.ts&path=src", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "a.ts") {
		t.Errorf("expected a.ts in results")
	}
	if strings.Contains(body, "b.ts") {
		t.Errorf("b.ts is outside path=src, should not appear")
	}
}

func TestFind_skipsGitAndNodeModules(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.MkdirAll(filepath.Join(root, ".git"), 0755)
	os.MkdirAll(filepath.Join(root, "node_modules"), 0755)
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte(""), 0644)
	os.WriteFile(filepath.Join(root, "node_modules", "lib.ts"), []byte(""), 0644)
	os.WriteFile(filepath.Join(root, "real.ts"), []byte(""), 0644)

	req := httptest.NewRequest(http.MethodGet, "/find?pattern=*.ts", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "node_modules") || strings.Contains(body, ".git") {
		t.Errorf("should not include .git or node_modules: %s", body)
	}
	if !strings.Contains(body, "real.ts") {
		t.Errorf("real.ts should appear")
	}
}

func TestFind_noMatch(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})

	req := httptest.NewRequest(http.MethodGet, "/find?pattern=*.doesnotexist", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No files found") {
		t.Errorf("expected no-match message, got: %s", w.Body.String())
	}
}

func TestFind_missingPattern(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodGet, "/find", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestFind_wrongMethod(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodPost, "/find?pattern=*.ts", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /grep — content search
// ---------------------------------------------------------------------------

func TestGrep_basicMatch(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "code.py"), []byte("# TODO: fix this\nsome_code()\n# another TODO here"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/grep?pattern=TODO", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "code.py:1:") {
		t.Errorf("expected match at line 1, got: %s", body)
	}
	if !strings.Contains(body, "code.py:3:") {
		t.Errorf("expected match at line 3, got: %s", body)
	}
}

func TestGrep_ignoreCase(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("Hello World\nhello world"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/grep?pattern=HELLO&ignore_case=true", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "f.txt:1:") || !strings.Contains(body, "f.txt:2:") {
		t.Errorf("ignore_case should match both lines, got: %s", body)
	}
}

func TestGrep_literal(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("price: $10.00\nno match"), 0644)

	// Without literal=true, $10.00 as regex would be invalid/odd; with literal it must match exactly.
	req := httptest.NewRequest(http.MethodGet, "/grep?pattern=$10.00&literal=true", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "f.txt:1:") {
		t.Errorf("literal match should find line 1, got: %s", w.Body.String())
	}
}

func TestGrep_globFilter(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "a.py"), []byte("TODO here"), 0644)
	os.WriteFile(filepath.Join(root, "b.ts"), []byte("TODO here"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/grep?pattern=TODO&glob=*.py", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "a.py") {
		t.Errorf("a.py should match glob *.py")
	}
	if strings.Contains(body, "b.ts") {
		t.Errorf("b.ts should be excluded by glob *.py")
	}
}

func TestGrep_withContext(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	content := "before\nmatch line\nafter"
	os.WriteFile(filepath.Join(root, "f.txt"), []byte(content), 0644)

	req := httptest.NewRequest(http.MethodGet, "/grep?pattern=match&context=1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	// Match line uses colon, context lines use dash
	if !strings.Contains(body, "f.txt:2:") {
		t.Errorf("expected match at line 2, got: %s", body)
	}
	if !strings.Contains(body, "f.txt-1-") || !strings.Contains(body, "f.txt-3-") {
		t.Errorf("expected context lines, got: %s", body)
	}
}

func TestGrep_noMatch(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	root := extractRoot(t, h)
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("nothing here"), 0644)

	req := httptest.NewRequest(http.MethodGet, "/grep?pattern=XYZNOTFOUND", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), "No matches found") {
		t.Errorf("expected no-match message, got: %s", w.Body.String())
	}
}

func TestGrep_invalidPattern(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodGet, "/grep?pattern=[invalid", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid regex, got %d", w.Code)
	}
}

func TestGrep_missingPattern(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodGet, "/grep", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGrep_wrongMethod(t *testing.T) {
	h := newTestHandler(t, &mockRuntime{})
	req := httptest.NewRequest(http.MethodPost, "/grep?pattern=foo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /exec?stream=true — NDJSON streaming
// ---------------------------------------------------------------------------

// mockRuntimeWithOutput writes predefined stdout/stderr before returning.
type mockRuntimeWithOutput struct {
	stdout   string
	stderr   string
	exitCode int
	execErr  error
}

func (m *mockRuntimeWithOutput) Name() string                                           { return "mock" }
func (m *mockRuntimeWithOutput) Run(opts container.RunOpts) error                       { return nil }
func (m *mockRuntimeWithOutput) Stop(name string) error                                 { return nil }
func (m *mockRuntimeWithOutput) Remove(name string, _ bool) error                       { return nil }
func (m *mockRuntimeWithOutput) Pull(image string) error                                { return nil }
func (m *mockRuntimeWithOutput) IsRunning(name string) (bool, error)                    { return true, nil }
func (m *mockRuntimeWithOutput) RunEphemeral(opts container.EphemeralOpts) (int, error) { return 0, nil }
func (m *mockRuntimeWithOutput) Status(prefix string) ([]container.ContainerStatus, error) {
	return nil, nil
}
func (m *mockRuntimeWithOutput) Exec(opts container.ExecOpts) (int, error) {
	if opts.Stdout != nil && m.stdout != "" {
		opts.Stdout.Write([]byte(m.stdout)) //nolint:errcheck
	}
	if opts.Stderr != nil && m.stderr != "" {
		opts.Stderr.Write([]byte(m.stderr)) //nolint:errcheck
	}
	return m.exitCode, m.execErr
}

func newTestHandlerWithOutput(t *testing.T, rt *mockRuntimeWithOutput) http.Handler {
	t.Helper()
	root := t.TempDir()
	dotDir := filepath.Join(root, ".toolbox")
	if err := os.MkdirAll(dotDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dotDir, "env"), []byte(""), 0644)

	cat := &catalog.Catalog{
		Containers: map[string]catalog.Container{
			"base": {Image: "base:latest", Fallback: true},
		},
	}
	mgr := &container.Manager{
		Runtime:       rt,
		WorkspaceRoot: root,
		Catalog:       cat,
	}
	return serve.NewHandler(mgr, fetch.Config{})
}

// parseNDJSON reads all NDJSON lines from body into a slice of raw maps.
func parseNDJSON(t *testing.T, body string) []map[string]any {
	t.Helper()
	var events []map[string]any
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid NDJSON line %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

func TestExecStream_queryParam(t *testing.T) {
	h := newTestHandlerWithOutput(t, &mockRuntimeWithOutput{stdout: "hello\n", exitCode: 0})
	body := `{"cmd":"echo hello"}`
	req := httptest.NewRequest(http.MethodPost, "/exec?stream=true", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/x-ndjson") {
		t.Errorf("want x-ndjson content-type, got %q", ct)
	}

	events := parseNDJSON(t, w.Body.String())
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d: %v", len(events), events)
	}

	// First event must be stdout chunk.
	if events[0]["type"] != "stdout" {
		t.Errorf("want first event type=stdout, got %v", events[0]["type"])
	}
	if events[0]["text"] != "hello\n" {
		t.Errorf("want text='hello\\n', got %v", events[0]["text"])
	}

	// Last event must be result.
	last := events[len(events)-1]
	if last["type"] != "result" {
		t.Errorf("want last event type=result, got %v", last["type"])
	}
	if last["exit"].(float64) != 0 {
		t.Errorf("want exit=0, got %v", last["exit"])
	}
}

func TestExecStream_acceptHeader(t *testing.T) {
	h := newTestHandlerWithOutput(t, &mockRuntimeWithOutput{stdout: "out", exitCode: 0})
	body := `{"cmd":"echo out"}`
	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(body))
	req.Header.Set("Accept", "application/x-ndjson")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/x-ndjson") {
		t.Errorf("want x-ndjson content-type, got %q", ct)
	}
	events := parseNDJSON(t, w.Body.String())
	last := events[len(events)-1]
	if last["type"] != "result" {
		t.Errorf("want result event, got %v", last["type"])
	}
}

func TestExecStream_stderrChunk(t *testing.T) {
	h := newTestHandlerWithOutput(t, &mockRuntimeWithOutput{stderr: "err msg\n", exitCode: 1})
	body := `{"cmd":"false"}`
	req := httptest.NewRequest(http.MethodPost, "/exec?stream=true", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	events := parseNDJSON(t, w.Body.String())

	var foundStderr bool
	for _, ev := range events {
		if ev["type"] == "stderr" {
			foundStderr = true
			if ev["text"] != "err msg\n" {
				t.Errorf("unexpected stderr text: %v", ev["text"])
			}
		}
	}
	if !foundStderr {
		t.Error("expected a stderr event")
	}

	last := events[len(events)-1]
	if last["type"] != "result" || last["exit"].(float64) != 1 {
		t.Errorf("want result exit=1, got %v", last)
	}
}

func TestExecStream_systemError(t *testing.T) {
	h := newTestHandlerWithOutput(t, &mockRuntimeWithOutput{execErr: fmt.Errorf("container crashed")})
	body := `{"cmd":"crash"}`
	req := httptest.NewRequest(http.MethodPost, "/exec?stream=true", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("streaming always returns 200 header, got %d", w.Code)
	}
	events := parseNDJSON(t, w.Body.String())
	last := events[len(events)-1]
	if last["type"] != "error" {
		t.Errorf("want error event, got %v", last["type"])
	}
	if !strings.Contains(last["error"].(string), "container crashed") {
		t.Errorf("unexpected error message: %v", last["error"])
	}
}

func TestExecStream_noOutputJustResult(t *testing.T) {
	h := newTestHandlerWithOutput(t, &mockRuntimeWithOutput{exitCode: 0})
	body := `{"cmd":"true"}`
	req := httptest.NewRequest(http.MethodPost, "/exec?stream=true", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	events := parseNDJSON(t, w.Body.String())
	if len(events) != 1 {
		t.Errorf("want exactly 1 event (result), got %d: %v", len(events), events)
	}
	if events[0]["type"] != "result" {
		t.Errorf("want result event, got %v", events[0]["type"])
	}
}

// ---------------------------------------------------------------------------
// extractRoot — reads the workspace path from /health so tests can seed files
// ---------------------------------------------------------------------------

func extractRoot(t *testing.T, h http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("health parse: %v", err)
	}
	root, _ := resp["workspace"].(string)
	if root == "" {
		t.Fatal("workspace not in /health response")
	}
	return root
}

// ---------------------------------------------------------------------------
// /fetch helpers
// ---------------------------------------------------------------------------

// newFetchHandler builds a serve handler backed by a fresh temp workspace and
// a custom fetch.Config. Returns the handler and the workspace root.
func newFetchHandler(t *testing.T, fetchCfg fetch.Config) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	dotDir := filepath.Join(root, ".toolbox")
	if err := os.MkdirAll(dotDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dotDir, "env"), []byte(""), 0644)
	cat := &catalog.Catalog{
		Containers: map[string]catalog.Container{
			"base": {Image: "base:latest", Fallback: true},
		},
	}
	mgr := &container.Manager{
		Runtime:       &mockRuntime{},
		WorkspaceRoot: root,
		Catalog:       cat,
	}
	return serve.NewHandler(mgr, fetchCfg), root
}

// htmlFetchServer starts an httptest.Server that serves minimal HTML.
// The handler records the last RequestURI received.
func htmlFetchServer(t *testing.T, html string) (*httptest.Server, *string) {
	t.Helper()
	received := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*received = r.RequestURI
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, html)
	}))
	t.Cleanup(srv.Close)
	return srv, received
}

// ---------------------------------------------------------------------------
// GET /fetch — error cases
// ---------------------------------------------------------------------------

func TestFetch_missingURLParam(t *testing.T) {
	h, _ := newFetchHandler(t, fetch.Config{})
	req := httptest.NewRequest(http.MethodGet, "/fetch", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 when url param missing, got %d — %s", w.Code, w.Body.String())
	}
}

func TestFetch_wrongMethod(t *testing.T) {
	h, _ := newFetchHandler(t, fetch.Config{})
	req := httptest.NewRequest(http.MethodPost, "/fetch?url=https://example.com", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

func TestFetch_cacheMissWithLines(t *testing.T) {
	// Requesting a line range before the URL has ever been fetched must return 400.
	h, _ := newFetchHandler(t, fetch.Config{})
	req := httptest.NewRequest(http.MethodGet,
		"/fetch?url=https://never-fetched.example.invalid&lines=1-10", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for cache miss with lines param, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /fetch — full fetch cycle (JSON summary)
// ---------------------------------------------------------------------------

func TestFetch_summaryJSON(t *testing.T) {
	srv, _ := htmlFetchServer(t, `<html><body>
<h1>Section One</h1><p>Content here.</p>
<h2>Section Two</h2><p>More content.</p>
</body></html>`)

	h, _ := newFetchHandler(t, fetch.Config{})
	req := httptest.NewRequest(http.MethodGet, "/fetch?url="+url.QueryEscape(srv.URL), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("want JSON content-type, got %q", ct)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v — body: %s", err, w.Body.String())
	}
	if resp["source"] != srv.URL {
		t.Errorf("source: want %q, got %v", srv.URL, resp["source"])
	}
	if resp["lines"] == nil {
		t.Error("response must include lines field")
	}
	if _, ok := resp["type"]; !ok {
		t.Error("response must include type field")
	}
	if _, ok := resp["generated"]; !ok {
		t.Error("response must include generated field")
	}
}

func TestFetch_summaryJSON_tocPresent(t *testing.T) {
	// HTML with headings must produce a non-empty toc in the JSON response.
	srv, _ := htmlFetchServer(t,
		`<html><body><h1>Intro</h1><p>text</p><h2>Details</h2><p>more</p></body></html>`)

	h, _ := newFetchHandler(t, fetch.Config{})
	req := httptest.NewRequest(http.MethodGet, "/fetch?url="+url.QueryEscape(srv.URL), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	toc, _ := resp["toc"].([]interface{})
	if len(toc) == 0 {
		t.Error("toc must be present when HTML contains headings")
	}
}

// ---------------------------------------------------------------------------
// GET /fetch?lines=N-M — line range from cache
// ---------------------------------------------------------------------------

func TestFetch_lineRange(t *testing.T) {
	srv, _ := htmlFetchServer(t,
		`<html><body><h1>Title</h1><p>Alpha.</p><p>Beta.</p><p>Gamma.</p></body></html>`)

	h, _ := newFetchHandler(t, fetch.Config{})
	target := url.QueryEscape(srv.URL)

	// First: populate the cache.
	req := httptest.NewRequest(http.MethodGet, "/fetch?url="+target, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("initial fetch: want 200, got %d — %s", w.Code, w.Body.String())
	}

	// Second: read a line range from the cache.
	req2 := httptest.NewRequest(http.MethodGet, "/fetch?url="+target+"&lines=1-3", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("line-range: want 200, got %d — %s", w2.Code, w2.Body.String())
	}
	if ct := w2.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("line-range response must be plain text, got %q", ct)
	}
	// Lines are returned as "  N  content"; at minimum line 1 must appear.
	if !strings.Contains(w2.Body.String(), "1") {
		t.Errorf("line-range body should contain line numbers, got: %s", w2.Body.String())
	}
}

func TestFetch_lineRange_cacheHit(t *testing.T) {
	// After the cache is populated, a line-range request must not trigger another HTTP fetch.
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h1>H</h1><p>line</p></body></html>")
	}))
	defer srv.Close()

	h, _ := newFetchHandler(t, fetch.Config{})
	target := url.QueryEscape(srv.URL)

	// Populate cache.
	req := httptest.NewRequest(http.MethodGet, "/fetch?url="+target, nil)
	httptest.NewRecorder()
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Line-range read — must hit cache only.
	req2 := httptest.NewRequest(http.MethodGet, "/fetch?url="+target+"&lines=1-2", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", w2.Code, w2.Body.String())
	}
	if requestCount != 1 {
		t.Errorf("want 1 HTTP request (line-range must use cache), got %d", requestCount)
	}
}

// ---------------------------------------------------------------------------
// GET /fetch — proxy_url routing via fetch.Config
// ---------------------------------------------------------------------------

func TestFetch_proxyURL_requestRoutedThroughProxy(t *testing.T) {
	// The proxy server captures requests intended for medium.com.
	proxy, receivedURI := htmlFetchServer(t,
		"<html><body><h1>Proxied Article</h1></body></html>")

	cfg := fetch.Config{
		Domains: map[string]fetch.DomainConfig{
			"medium.com": {ProxyURL: proxy.URL},
		},
	}
	h, _ := newFetchHandler(t, cfg)

	originalURL := "https://medium.com/@author/article-title"
	req := httptest.NewRequest(http.MethodGet, "/fetch?url="+url.QueryEscape(originalURL), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", w.Code, w.Body.String())
	}

	// The proxy server must have received a request whose path encodes the original URL.
	if !strings.Contains(*receivedURI, "medium.com") {
		t.Errorf("proxy server did not receive the medium.com URL; RequestURI = %q", *receivedURI)
	}

	// The JSON response source must be the original URL, not the proxy URL.
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["source"] != originalURL {
		t.Errorf("source: want original URL %q, got %v", originalURL, resp["source"])
	}
}

func TestFetch_proxyURL_nonMediumURLUnaffected(t *testing.T) {
	// A URL from a domain with no proxy config must be fetched directly.
	directSrv, directURI := htmlFetchServer(t,
		"<html><body><h1>Direct</h1></body></html>")

	// Proxy is configured only for medium.com.
	cfg := fetch.Config{
		Domains: map[string]fetch.DomainConfig{
			"medium.com": {ProxyURL: "https://should-not-be-called.invalid"},
		},
	}
	h, _ := newFetchHandler(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/fetch?url="+url.QueryEscape(directSrv.URL), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — %s", w.Code, w.Body.String())
	}
	// Direct server must have been called (not proxied).
	if *directURI == "" {
		t.Error("direct server should have received the request")
	}
}
