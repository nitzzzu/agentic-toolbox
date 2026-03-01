package log

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry is a single record written to the exec log.
type Entry struct {
	TS        time.Time `json:"ts"`
	Container string    `json:"container"`
	Image     string    `json:"image"`
	Command   string    `json:"cmd"`
	Ephemeral bool      `json:"ephemeral,omitempty"`
	ExitCode  int       `json:"exit"`
	Ms        int64     `json:"ms"`
}

// LogPath returns the path to the exec log file.
func LogPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".toolbox", "exec.log")
}

// Append writes one JSON line to the exec log.
func Append(workspaceRoot string, e Entry) error {
	path := LogPath(workspaceRoot)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// Read reads all log entries, returning the last tail entries.
// tail=0 returns all entries.
func Read(workspaceRoot string, tail int) ([]Entry, error) {
	data, err := os.ReadFile(LogPath(workspaceRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []Entry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, e)
	}

	if tail > 0 && len(entries) > tail {
		entries = entries[len(entries)-tail:]
	}
	return entries, nil
}
