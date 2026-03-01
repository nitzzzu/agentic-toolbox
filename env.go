package env

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/toolbox-tools/toolbox/internal/catalog"
)

// Merged builds the final environment for a container exec.
// Priority (highest wins):
//  1. Per-container env from catalog
//  2. .toolbox/env.local  (gitignored secrets)
//  3. .toolbox/env        (committed shared config)
//  4. Host environment    (filtered by forward/deny rules)
//
// Returns a slice of "KEY=VALUE" strings suitable for --env flags.
func Merged(
	workspaceRoot string,
	rules catalog.EnvRules,
	containerEnv map[string]string,
) ([]string, error) {
	// Start with filtered host env.
	merged := filterHostEnv(rules)

	// Layer .toolbox/env on top.
	if err := layerEnvFile(merged, filepath.Join(workspaceRoot, ".toolbox", "env")); err != nil {
		return nil, fmt.Errorf(".toolbox/env: %w", err)
	}

	// Layer .toolbox/env.local on top (may not exist — that's fine).
	localPath := filepath.Join(workspaceRoot, ".toolbox", "env.local")
	if _, err := os.Stat(localPath); err == nil {
		if err := layerEnvFile(merged, localPath); err != nil {
			return nil, fmt.Errorf(".toolbox/env.local: %w", err)
		}
	}

	// Layer per-container env last (highest priority, allows container-specific overrides).
	for k, v := range containerEnv {
		merged[k] = interpolate(v)
	}

	// Serialize.
	result := make([]string, 0, len(merged))
	for k, v := range merged {
		result = append(result, k+"="+v)
	}
	return result, nil
}

// filterHostEnv applies forward/deny rules to the current host environment.
func filterHostEnv(rules catalog.EnvRules) map[string]string {
	out := make(map[string]string)

	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]

		// Must match at least one forward pattern (if any are defined).
		if len(rules.Forward) > 0 && !matchesAny(key, rules.Forward) {
			continue
		}

		// Must not match any deny pattern.
		if matchesAny(key, rules.Deny) {
			continue
		}

		out[key] = parts[1]
	}

	return out
}

// layerEnvFile reads a dotenv-style file and adds/overwrites keys in dst.
// Format: KEY=VALUE, lines starting with # are comments, blank lines ignored.
func layerEnvFile(dst map[string]string, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("line %d: invalid format %q (expected KEY=VALUE)", lineNum, line)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Strip surrounding quotes if present.
		val = stripQuotes(val)
		dst[key] = interpolate(val)
	}
	return scanner.Err()
}

// matchesAny returns true if key matches any of the glob patterns.
// Supports only '*' wildcard (e.g., "AWS_*", "OPENAI_*").
func matchesAny(key string, patterns []string) bool {
	for _, p := range patterns {
		if globMatch(p, key) {
			return true
		}
	}
	return false
}

// globMatch implements simple glob matching with '*' wildcard.
func globMatch(pattern, s string) bool {
	// No wildcard — exact match.
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}

	parts := strings.Split(pattern, "*")
	if len(parts) == 2 {
		// PREFIX* — prefix match.
		if parts[1] == "" {
			return strings.HasPrefix(s, parts[0])
		}
		// *SUFFIX — suffix match.
		if parts[0] == "" {
			return strings.HasSuffix(s, parts[1])
		}
		// PREFIX*SUFFIX
		return strings.HasPrefix(s, parts[0]) && strings.HasSuffix(s, parts[1])
	}

	// Multiple wildcards — fall back to strings.Contains check per segment.
	pos := 0
	for i, part := range parts {
		if i == 0 {
			if !strings.HasPrefix(s, part) {
				return false
			}
			pos = len(part)
			continue
		}
		idx := strings.Index(s[pos:], part)
		if idx < 0 {
			return false
		}
		pos += idx + len(part)
	}
	return true
}

// stripQuotes removes surrounding single or double quotes.
func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// interpolate expands ${VAR} references in env values using the host env.
func interpolate(s string) string {
	return os.Expand(s, os.Getenv)
}

// ParseEnvFile reads a .toolbox/env file and returns a map.
func ParseEnvFile(path string) (map[string]string, error) {
	result := make(map[string]string)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return result, nil
	}
	if err := layerEnvFile(result, path); err != nil {
		return nil, err
	}
	return result, nil
}

// WriteEnvFile writes a map of key/value pairs to a dotenv file,
// replacing the file entirely (preserves no prior content).
func WriteEnvFile(path string, vars map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for k, v := range vars {
		fmt.Fprintf(w, "%s=%s\n", k, v)
	}
	return w.Flush()
}

// UpdateEnvFile reads the existing file, merges new vars, and writes back.
func UpdateEnvFile(path string, vars map[string]string) error {
	existing := make(map[string]string)
	// Preserve existing entries.
	_ = layerEnvFile(existing, path)
	for k, v := range vars {
		existing[k] = v
	}
	return WriteEnvFile(path, existing)
}

// DeleteFromEnvFile removes a key from a dotenv file.
func DeleteFromEnvFile(path, key string) error {
	vars := make(map[string]string)
	if err := layerEnvFile(vars, path); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(vars, key)
	return WriteEnvFile(path, vars)
}
