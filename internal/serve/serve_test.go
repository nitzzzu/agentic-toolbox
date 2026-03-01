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
