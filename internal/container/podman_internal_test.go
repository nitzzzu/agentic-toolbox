package container

// Internal (white-box) tests for execWithOpts.
// These run real subprocesses — requires sh to be available (Unix / Git Bash).

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecWithOpts_success(t *testing.T) {
	var out bytes.Buffer
	code, err := execWithOpts(0, &out, nil, "go", "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("want exit code 0, got %d", code)
	}
	if !strings.Contains(out.String(), "go version") {
		t.Errorf("expected 'go version' in output, got: %q", out.String())
	}
}

func TestExecWithOpts_nonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	code, err := execWithOpts(0, nil, nil, "sh", "-c", "exit 42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 42 {
		t.Errorf("want exit code 42, got %d", code)
	}
}

func TestExecWithOpts_captureStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	var out bytes.Buffer
	_, err := execWithOpts(0, &out, nil, "sh", "-c", "echo toolbox-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "toolbox-test") {
		t.Errorf("expected captured output, got: %q", out.String())
	}
}

func TestExecWithOpts_captureStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	var errOut bytes.Buffer
	_, _ = execWithOpts(0, nil, &errOut, "sh", "-c", "echo err-output >&2")
	if !strings.Contains(errOut.String(), "err-output") {
		t.Errorf("expected captured stderr, got: %q", errOut.String())
	}
}

func TestExecWithOpts_timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	code, err := execWithOpts(50*time.Millisecond, nil, nil, "sh", "-c", "sleep 60")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected 'timed out' in error, got: %v", err)
	}
	if code != 1 {
		t.Errorf("want exit code 1 on timeout, got %d", code)
	}
}

func TestExecWithOpts_missingBinary(t *testing.T) {
	code, err := execWithOpts(0, nil, nil, "this-binary-does-not-exist-anywhere")
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if code != 1 {
		t.Errorf("want exit code 1, got %d", code)
	}
}
