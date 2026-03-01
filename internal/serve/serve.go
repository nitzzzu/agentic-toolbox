package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/toolbox-tools/toolbox/internal/container"
	execlog "github.com/toolbox-tools/toolbox/internal/log"
)

type execRequest struct {
	Cmd       string `json:"cmd"`
	Container string `json:"container"`
	Timeout   string `json:"timeout"`
	Ephemeral bool   `json:"ephemeral"`
}

type execResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit"`
	Ms       int64  `json:"ms"`
}

type healthResponse struct {
	OK        bool   `json:"ok"`
	Workspace string `json:"workspace"`
	Runtime   string `json:"runtime"`
}

// NewHandler builds the HTTP mux for the serve API (useful for testing).
func NewHandler(mgr *container.Manager) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, healthResponse{
			OK:        true,
			Workspace: mgr.WorkspaceRoot,
			Runtime:   mgr.Runtime.Name(),
		})
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		statuses, err := mgr.Status()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, statuses)
	})

	mux.HandleFunc("/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req execRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Cmd == "" {
			http.Error(w, `"cmd" is required`, http.StatusBadRequest)
			return
		}

		var timeout time.Duration
		if req.Timeout != "" {
			timeout, _ = time.ParseDuration(req.Timeout)
		}

		var outBuf, errBuf bytes.Buffer
		start := time.Now()
		exitCode, execErr := mgr.ExecCommand(container.ExecOptions{
			Command:        req.Cmd,
			ForceContainer: req.Container,
			Timeout:        timeout,
			Ephemeral:      req.Ephemeral,
			Stdout:         &outBuf,
			Stderr:         &errBuf,
		})
		elapsed := time.Since(start)

		// Append to exec log (best-effort).
		_ = execlog.Append(mgr.WorkspaceRoot, execlog.Entry{
			TS:        start,
			Container: req.Container,
			Command:   req.Cmd,
			Ephemeral: req.Ephemeral,
			ExitCode:  exitCode,
			Ms:        elapsed.Milliseconds(),
		})

		if execErr != nil {
			http.Error(w, execErr.Error(), http.StatusInternalServerError)
			return
		}

		writeJSON(w, execResponse{
			Stdout:   outBuf.String(),
			Stderr:   errBuf.String(),
			ExitCode: exitCode,
			Ms:       elapsed.Milliseconds(),
		})
	})

	return mux
}

// Serve starts the HTTP API server on 127.0.0.1:port.
func Serve(mgr *container.Manager, port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("[toolbox serve] listening on http://%s\n", addr)
	return http.ListenAndServe(addr, NewHandler(mgr))
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
