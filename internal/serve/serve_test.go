package serve_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolbox-tools/toolbox/internal/catalog"
	"github.com/toolbox-tools/toolbox/internal/container"
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
	return serve.NewHandler(mgr)
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
