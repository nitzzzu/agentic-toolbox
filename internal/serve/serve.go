package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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

type fileEntry struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	IsDir    bool      `json:"is_dir"`
}

type listResponse struct {
	Path    string      `json:"path"`
	Entries []fileEntry `json:"entries"`
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

	// /workspace  — list directory at optional ?path= query param.
	mux.HandleFunc("/workspace", workspaceListHandler(mgr.WorkspaceRoot))

	// /workspace/ — file CRUD: GET (read), PUT (write), DELETE.
	//               GET supports ?offset=N&limit=N for line-ranged reads (1-indexed).
	mux.HandleFunc("/workspace/", workspaceFileHandler(mgr.WorkspaceRoot))

	// /find — glob file search across the workspace.
	//         GET ?pattern=**/*.ts&path=src&limit=1000
	mux.HandleFunc("/find", workspaceFindHandler(mgr.WorkspaceRoot))

	// /grep — regex/literal content search across workspace files.
	//         GET ?pattern=TODO&path=src&glob=*.ts&ignore_case=true&context=2&limit=100
	mux.HandleFunc("/grep", workspaceGrepHandler(mgr.WorkspaceRoot))

	return mux
}

// Serve starts the HTTP API server on 127.0.0.1:port.
func Serve(mgr *container.Manager, port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("[toolbox serve] listening on http://%s\n", addr)
	return http.ListenAndServe(addr, NewHandler(mgr))
}

// ---------------------------------------------------------------------------
// /workspace  — directory listing
// ---------------------------------------------------------------------------

func workspaceListHandler(root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		subPath := r.URL.Query().Get("path")
		dirPath, err := safeJoin(root, subPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		entries, err := os.ReadDir(dirPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "path not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		resp := listResponse{Path: subPath, Entries: make([]fileEntry, 0, len(entries))}
		for _, e := range entries {
			info, _ := e.Info()
			var size int64
			var mod time.Time
			if info != nil {
				size = info.Size()
				mod = info.ModTime()
			}
			resp.Entries = append(resp.Entries, fileEntry{
				Name:     e.Name(),
				Size:     size,
				Modified: mod,
				IsDir:    e.IsDir(),
			})
		}
		writeJSON(w, resp)
	}
}

// ---------------------------------------------------------------------------
// /workspace/{path}  — file CRUD
// ---------------------------------------------------------------------------

func workspaceFileHandler(root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relPath := strings.TrimPrefix(r.URL.Path, "/workspace/")

		fullPath, err := safeJoin(root, relPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodGet:
			data, err := os.ReadFile(fullPath)
			if err != nil {
				if os.IsNotExist(err) {
					http.Error(w, "file not found", http.StatusNotFound)
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			// Optional line-range: ?offset=N&limit=N (1-indexed offset).
			offsetStr := r.URL.Query().Get("offset")
			limitStr := r.URL.Query().Get("limit")
			if offsetStr != "" || limitStr != "" {
				lines := strings.Split(string(data), "\n")
				total := len(lines)

				start := 0
				if offsetStr != "" {
					if n, parseErr := strconv.Atoi(offsetStr); parseErr == nil && n > 0 {
						start = n - 1 // 1-indexed → 0-indexed
					}
				}
				if start >= total {
					start = total
				}

				end := total
				if limitStr != "" {
					if n, parseErr := strconv.Atoi(limitStr); parseErr == nil && n > 0 {
						if start+n < total {
							end = start + n
						}
					}
				}

				data = []byte(strings.Join(lines[start:end], "\n"))
			}

			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(data)

		case http.MethodPut:
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				http.Error(w, "cannot create directories: "+err.Error(), http.StatusInternalServerError)
				return
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "cannot read body: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := os.WriteFile(fullPath, data, 0644); err != nil {
				http.Error(w, "cannot write file: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)

		case http.MethodDelete:
			if err := os.Remove(fullPath); err != nil {
				if os.IsNotExist(err) {
					http.Error(w, "file not found", http.StatusNotFound)
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// ---------------------------------------------------------------------------
// /find  — glob file search
// ---------------------------------------------------------------------------

// workspaceFindHandler lists files matching a glob pattern under the workspace.
//
// Query params:
//
//	pattern  (required) — glob, e.g. "*.py", "**/*.ts", "data/*.csv"
//	path     (optional) — subdirectory to restrict search to
//	limit    (optional) — max results, default 1000
//
// Skips .git/ and node_modules/ automatically.
// Returns relative paths, one per line.
func workspaceFindHandler(root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		pattern := r.URL.Query().Get("pattern")
		if pattern == "" {
			http.Error(w, `"pattern" is required`, http.StatusBadRequest)
			return
		}

		subPath := r.URL.Query().Get("path")
		limit := 1000
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
				limit = n
			}
		}

		searchRoot, err := safeJoin(root, subPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var matches []string
		limitReached := false

		_ = filepath.WalkDir(searchRoot, func(absPath string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}

			rel, err := filepath.Rel(searchRoot, absPath)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			if matchGlobPath(pattern, rel) {
				matches = append(matches, rel)
				if len(matches) >= limit {
					limitReached = true
					return fs.SkipAll
				}
			}
			return nil
		})

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if len(matches) == 0 {
			_, _ = fmt.Fprintln(w, "No files found matching pattern")
			return
		}

		var sb strings.Builder
		for _, m := range matches {
			sb.WriteString(m)
			sb.WriteByte('\n')
		}
		if limitReached {
			sb.WriteString(fmt.Sprintf("\n[%d results limit reached. Refine pattern or use limit=%d for more]",
				limit, limit*2))
		}
		_, _ = io.WriteString(w, sb.String())
	}
}

// ---------------------------------------------------------------------------
// /grep  — content search
// ---------------------------------------------------------------------------

// workspaceGrepHandler searches file contents using Go regexp.
//
// Query params:
//
//	pattern     (required) — regex or literal string
//	path        (optional) — subdirectory or file to search
//	glob        (optional) — filename filter, e.g. "*.py"
//	ignore_case (optional) — "true" for case-insensitive
//	literal     (optional) — "true" to escape pattern as literal string
//	context     (optional) — lines of context before/after each match (default 0)
//	limit       (optional) — max matches, default 100
//
// Output format matches ripgrep:
//
//	file:line: matching line text     ← match line
//	file-line- context line text      ← context line
const grepMaxLineLen = 500

func workspaceGrepHandler(root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		pattern := r.URL.Query().Get("pattern")
		if pattern == "" {
			http.Error(w, `"pattern" is required`, http.StatusBadRequest)
			return
		}

		subPath := r.URL.Query().Get("path")
		glob := r.URL.Query().Get("glob")
		ignoreCase := r.URL.Query().Get("ignore_case") == "true"
		literal := r.URL.Query().Get("literal") == "true"

		contextLines := 0
		if ctxStr := r.URL.Query().Get("context"); ctxStr != "" {
			if n, err := strconv.Atoi(ctxStr); err == nil && n > 0 {
				contextLines = n
			}
		}
		limit := 100
		if limStr := r.URL.Query().Get("limit"); limStr != "" {
			if n, err := strconv.Atoi(limStr); err == nil && n > 0 {
				limit = n
			}
		}

		// Build regexp.
		pat := pattern
		if literal {
			pat = regexp.QuoteMeta(pat)
		}
		if ignoreCase {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			http.Error(w, "invalid pattern: "+err.Error(), http.StatusBadRequest)
			return
		}

		searchRoot, err := safeJoin(root, subPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var sb strings.Builder
		matchCount := 0
		limitReached := false

		truncLine := func(s string) string {
			if len(s) > grepMaxLineLen {
				return s[:grepMaxLineLen] + "... [truncated]"
			}
			return s
		}

		_ = filepath.WalkDir(searchRoot, func(absPath string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || limitReached {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}

			// Glob filter applies to the file base name.
			if glob != "" {
				ok, _ := filepath.Match(glob, d.Name())
				if !ok {
					return nil
				}
			}

			rel, _ := filepath.Rel(searchRoot, absPath)
			rel = filepath.ToSlash(rel)

			data, readErr := os.ReadFile(absPath)
			if readErr != nil {
				return nil
			}

			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				if !re.MatchString(line) {
					continue
				}
				matchCount++
				if matchCount > limit {
					limitReached = true
					break
				}

				lineNum := i + 1 // 1-indexed

				// Context before.
				start := max(0, i-contextLines)
				for c := start; c < i; c++ {
					sb.WriteString(fmt.Sprintf("%s-%d- %s\n", rel, c+1, truncLine(lines[c])))
				}

				// Match line.
				sb.WriteString(fmt.Sprintf("%s:%d: %s\n", rel, lineNum, truncLine(line)))

				// Context after.
				end := min(len(lines)-1, i+contextLines)
				for c := i + 1; c <= end; c++ {
					sb.WriteString(fmt.Sprintf("%s-%d- %s\n", rel, c+1, truncLine(lines[c])))
				}
			}
			return nil
		})

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if matchCount == 0 {
			_, _ = fmt.Fprintln(w, "No matches found")
			return
		}
		if limitReached {
			sb.WriteString(fmt.Sprintf("\n[%d matches limit reached. Use limit=%d for more, or refine pattern]",
				limit, limit*2))
		}
		_, _ = io.WriteString(w, sb.String())
	}
}

// ---------------------------------------------------------------------------
// Glob helpers
// ---------------------------------------------------------------------------

// matchGlobPath reports whether relPath matches pattern.
// Supports ** for matching any number of path segments.
// For patterns without /, matches against the base filename only.
func matchGlobPath(pattern, relPath string) bool {
	pattern = filepath.ToSlash(pattern)
	relPath = filepath.ToSlash(relPath)

	// Simple filename pattern (no directory component).
	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, path.Base(relPath))
		return ok
	}

	// Pattern with **: recursive segment matching.
	if strings.Contains(pattern, "**") {
		return matchDoubleStarSegments(
			strings.Split(pattern, "/"),
			strings.Split(relPath, "/"),
		)
	}

	// Full path match.
	ok, _ := path.Match(pattern, relPath)
	return ok
}

// matchDoubleStarSegments recursively matches glob segments where ** absorbs
// zero or more path segments.
func matchDoubleStarSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			pat = pat[1:]
			if len(pat) == 0 {
				return true // ** at end matches everything remaining
			}
			for i := 0; i <= len(name); i++ {
				if matchDoubleStarSegments(pat, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, _ := path.Match(pat[0], name[0])
		if !ok {
			return false
		}
		pat = pat[1:]
		name = name[1:]
	}
	return len(name) == 0
}

// ---------------------------------------------------------------------------
// Path safety
// ---------------------------------------------------------------------------

// safeJoin joins root and requestPath, returning an error if the result
// escapes outside root (path traversal protection).
func safeJoin(root, requestPath string) (string, error) {
	full := filepath.Join(root, filepath.FromSlash(requestPath))
	rel, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path outside workspace")
	}
	return full, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
