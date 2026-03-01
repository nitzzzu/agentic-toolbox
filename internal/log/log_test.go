package log_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	execlog "github.com/toolbox-tools/toolbox/internal/log"
)

func tempWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".toolbox"), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

// ---------------------------------------------------------------------------
// LogPath
// ---------------------------------------------------------------------------

func TestLogPath(t *testing.T) {
	path := execlog.LogPath("/my/workspace")
	want := filepath.Join("/my/workspace", ".toolbox", "exec.log")
	if path != want {
		t.Errorf("want %q, got %q", want, path)
	}
}

// ---------------------------------------------------------------------------
// Append + Read roundtrip
// ---------------------------------------------------------------------------

func TestAppendAndRead(t *testing.T) {
	root := tempWorkspace(t)
	now := time.Now().UTC().Truncate(time.Second)

	entry := execlog.Entry{
		TS:        now,
		Container: "base",
		Image:     "test:latest",
		Command:   "python3 train.py",
		Ephemeral: false,
		ExitCode:  0,
		Ms:        1234,
	}

	if err := execlog.Append(root, entry); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries, err := execlog.Read(root, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}

	got := entries[0]
	if got.Container != entry.Container {
		t.Errorf("Container: want %q, got %q", entry.Container, got.Container)
	}
	if got.Command != entry.Command {
		t.Errorf("Command: want %q, got %q", entry.Command, got.Command)
	}
	if got.ExitCode != entry.ExitCode {
		t.Errorf("ExitCode: want %d, got %d", entry.ExitCode, got.ExitCode)
	}
	if got.Ms != entry.Ms {
		t.Errorf("Ms: want %d, got %d", entry.Ms, got.Ms)
	}
}

func TestAppend_multiple(t *testing.T) {
	root := tempWorkspace(t)

	for i := 0; i < 5; i++ {
		if err := execlog.Append(root, execlog.Entry{
			TS:      time.Now(),
			Command: "cmd",
			Ms:      int64(i),
		}); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}

	entries, err := execlog.Read(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Errorf("want 5 entries, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Read with tail
// ---------------------------------------------------------------------------

func TestRead_tail(t *testing.T) {
	root := tempWorkspace(t)

	for i := 0; i < 10; i++ {
		_ = execlog.Append(root, execlog.Entry{TS: time.Now(), Ms: int64(i)})
	}

	entries, err := execlog.Read(root, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("want 3 entries with tail=3, got %d", len(entries))
	}
	// Should be the last 3 entries (Ms=7,8,9).
	if entries[0].Ms != 7 || entries[2].Ms != 9 {
		t.Errorf("tail should return last entries: got Ms=%v", []int64{entries[0].Ms, entries[1].Ms, entries[2].Ms})
	}
}

func TestRead_tailLargerThanTotal(t *testing.T) {
	root := tempWorkspace(t)
	_ = execlog.Append(root, execlog.Entry{TS: time.Now()})

	entries, err := execlog.Read(root, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("want 1 entry, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Missing / empty file
// ---------------------------------------------------------------------------

func TestRead_missingFile(t *testing.T) {
	root := tempWorkspace(t)
	entries, err := execlog.Read(root, 0)
	if err != nil {
		t.Fatalf("Read on missing log should not error, got: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries for missing log, got %v", entries)
	}
}

// ---------------------------------------------------------------------------
// Ephemeral flag preserved
// ---------------------------------------------------------------------------

func TestAppend_ephemeral(t *testing.T) {
	root := tempWorkspace(t)
	_ = execlog.Append(root, execlog.Entry{
		TS:        time.Now(),
		Command:   "hostname",
		Ephemeral: true,
		ExitCode:  0,
	})

	entries, _ := execlog.Read(root, 0)
	if len(entries) != 1 {
		t.Fatal("expected 1 entry")
	}
	if !entries[0].Ephemeral {
		t.Error("Ephemeral flag should be preserved")
	}
}
