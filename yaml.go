// Package yaml provides a minimal YAML parser for catalog.yaml.
// It handles the subset of YAML used by toolbox: maps, sequences,
// strings, booleans, and integers. No anchors, no complex types.
package yaml

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Unmarshal parses YAML data into v (must be a pointer to a struct or map).
func Unmarshal(data []byte, v interface{}) error {
	lines := splitLines(string(data))
	tokens := tokenize(lines)
	val, _, err := parseValue(tokens, 0, 0)
	if err != nil {
		return err
	}
	return assign(val, reflect.ValueOf(v))
}

// ---------------------------------------------------------------------------
// Tokenizer
// ---------------------------------------------------------------------------

type token struct {
	indent  int
	key     string // non-empty if this is a mapping key line
	value   string // scalar value (may be on same line as key, or standalone)
	isList  bool   // line starts with "- "
	lineNum int
}

func splitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

func tokenize(lines []string) []token {
	var tokens []token
	for i, line := range lines {
		// Strip comments.
		if ci := commentIndex(line); ci >= 0 {
			line = line[:ci]
		}
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			continue
		}
		indent := leadingSpaces(trimmed)
		content := strings.TrimSpace(trimmed)

		t := token{indent: indent, lineNum: i + 1}

		if strings.HasPrefix(content, "- ") {
			t.isList = true
			t.value = strings.TrimSpace(content[2:])
		} else if idx := strings.Index(content, ": "); idx >= 0 {
			t.key = content[:idx]
			t.value = strings.TrimSpace(content[idx+2:])
		} else if strings.HasSuffix(content, ":") {
			t.key = content[:len(content)-1]
		} else {
			t.value = content
		}

		tokens = append(tokens, t)
	}
	return tokens
}

func commentIndex(line string) int {
	inSingle, inDouble := false, false
	for i, ch := range line {
		switch ch {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return i
			}
		}
	}
	return -1
}

func leadingSpaces(s string) int {
	n := 0
	for _, ch := range s {
		if ch == ' ' {
			n++
		} else {
			break
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

// parseValue returns a parsed value and the number of tokens consumed.
// parentIndent is the indent level of the parent context.
func parseValue(tokens []token, pos, parentIndent int) (interface{}, int, error) {
	if pos >= len(tokens) {
		return nil, 0, nil
	}
	t := tokens[pos]

	if t.isList {
		return parseList(tokens, pos, t.indent)
	}
	if t.key != "" {
		return parseMap(tokens, pos, t.indent)
	}
	return parseScalar(t.value), 1, nil
}

func parseMap(tokens []token, pos, mapIndent int) (map[string]interface{}, int, error) {
	result := make(map[string]interface{})
	consumed := 0
	for pos+consumed < len(tokens) {
		t := tokens[pos+consumed]
		if t.indent < mapIndent {
			break
		}
		if t.indent > mapIndent {
			break
		}
		if t.key == "" {
			break
		}
		consumed++
		key := t.key

		// Value on same line?
		if t.value != "" {
			result[key] = parseScalar(t.value)
			continue
		}

		// Value on next lines (nested).
		if pos+consumed < len(tokens) {
			next := tokens[pos+consumed]
			if next.indent > mapIndent {
				val, c, err := parseValue(tokens, pos+consumed, t.indent)
				if err != nil {
					return nil, 0, err
				}
				result[key] = val
				consumed += c
				continue
			}
		}
		result[key] = nil
	}
	return result, consumed, nil
}

func parseList(tokens []token, pos, listIndent int) ([]interface{}, int, error) {
	var result []interface{}
	consumed := 0
	for pos+consumed < len(tokens) {
		t := tokens[pos+consumed]
		if t.indent < listIndent {
			break
		}
		if !t.isList {
			break
		}
		consumed++

		// Inline scalar value?
		if t.value != "" {
			// Check if value looks like a map entry (key: val).
			if idx := strings.Index(t.value, ": "); idx >= 0 {
				// Inline map in list item.
				subMap := map[string]interface{}{
					t.value[:idx]: parseScalar(t.value[idx+2:]),
				}
				result = append(result, subMap)
			} else {
				result = append(result, parseScalar(t.value))
			}
			continue
		}

		// Multi-line list item (nested map).
		if pos+consumed < len(tokens) {
			next := tokens[pos+consumed]
			if next.indent > listIndent {
				val, c, err := parseValue(tokens, pos+consumed, listIndent)
				if err != nil {
					return nil, 0, err
				}
				result = append(result, val)
				consumed += c
				continue
			}
		}
		result = append(result, nil)
	}
	return result, consumed, nil
}

func parseScalar(s string) interface{} {
	// Strip quotes.
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	// Boolean.
	switch strings.ToLower(s) {
	case "true", "yes", "on":
		return true
	case "false", "no", "off":
		return false
	case "null", "~", "":
		return nil
	}
	// Integer.
	if i, err := strconv.Atoi(s); err == nil {
		return i
	}
	return s
}

// ---------------------------------------------------------------------------
// Struct assignment via reflection
// ---------------------------------------------------------------------------

func assign(src interface{}, dst reflect.Value) error {
	// Dereference pointer.
	for dst.Kind() == reflect.Ptr {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		dst = dst.Elem()
	}

	if src == nil {
		return nil
	}

	switch dst.Kind() {
	case reflect.Struct:
		m, ok := src.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected map for struct, got %T", src)
		}
		return assignStruct(m, dst)

	case reflect.Map:
		m, ok := src.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected map, got %T", src)
		}
		if dst.IsNil() {
			dst.Set(reflect.MakeMap(dst.Type()))
		}
		for k, v := range m {
			kv := reflect.ValueOf(k)
			vv := reflect.New(dst.Type().Elem()).Elem()
			if err := assign(v, vv); err != nil {
				return err
			}
			dst.SetMapIndex(kv, vv)
		}
		return nil

	case reflect.Slice:
		list, ok := src.([]interface{})
		if !ok {
			return fmt.Errorf("expected list, got %T", src)
		}
		sl := reflect.MakeSlice(dst.Type(), len(list), len(list))
		for i, item := range list {
			if err := assign(item, sl.Index(i)); err != nil {
				return err
			}
		}
		dst.Set(sl)
		return nil

	case reflect.String:
		switch v := src.(type) {
		case string:
			dst.SetString(v)
		case bool:
			dst.SetString(strconv.FormatBool(v))
		case int:
			dst.SetString(strconv.Itoa(v))
		}
		return nil

	case reflect.Bool:
		switch v := src.(type) {
		case bool:
			dst.SetBool(v)
		case string:
			b, _ := strconv.ParseBool(v)
			dst.SetBool(b)
		}
		return nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch v := src.(type) {
		case int:
			dst.SetInt(int64(v))
		case string:
			i, _ := strconv.ParseInt(v, 10, 64)
			dst.SetInt(i)
		}
		return nil

	default:
		return fmt.Errorf("unsupported kind %s", dst.Kind())
	}
}

func assignStruct(m map[string]interface{}, dst reflect.Value) error {
	t := dst.Type()
	// Build a map from yaml tag → field index.
	tagMap := make(map[string]int)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("yaml")
		if tag == "" {
			tag = strings.ToLower(f.Name)
		} else {
			tag = strings.Split(tag, ",")[0]
		}
		tagMap[tag] = i
	}

	for k, v := range m {
		idx, ok := tagMap[k]
		if !ok {
			// Unknown field — ignore (like json.Unmarshal).
			continue
		}
		field := dst.Field(idx)
		if err := assign(v, field); err != nil {
			return fmt.Errorf("field %q: %w", k, err)
		}
	}
	return nil
}
