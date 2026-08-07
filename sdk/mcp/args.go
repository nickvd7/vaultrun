package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// coerceToolArgs decodes tool arguments flexibly. MCP hosts often send typed
// JSON (bools, numbers, nested objects/arrays). Handlers historically expect
// map[string]string, so nested values are re-encoded as JSON strings.
func coerceToolArgs(raw json.RawMessage) (map[string]string, error) {
	out := make(map[string]string)
	if len(raw) == 0 {
		return out, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	for k, v := range m {
		s, err := anyToToolArgString(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = s
	}
	return out, nil
}

func anyToToolArgString(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 64), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case json.Number:
		return x.String(), nil
	case map[string]any, []any:
		b, err := json.Marshal(x)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		// Fallback: try JSON first (structs), else Sprint.
		b, err := json.Marshal(x)
		if err == nil && len(b) > 0 && b[0] != '"' {
			return string(b), nil
		}
		if err == nil && len(b) >= 2 && b[0] == '"' {
			var s string
			if json.Unmarshal(b, &s) == nil {
				return s, nil
			}
		}
		return fmt.Sprint(x), nil
	}
}

func anyString(v any) string {
	s, err := anyToToolArgString(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return s
}

func anyFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case float32:
		return float64(x), nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case json.Number:
		return x.Float64()
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, fmt.Errorf("empty")
		}
		return strconv.ParseFloat(s, 64)
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}

func anyBoolString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		switch s {
		case "true", "1", "yes":
			return "true"
		case "false", "0", "no":
			return "false"
		case "":
			return ""
		default:
			return s
		}
	case float64:
		if x == 1 {
			return "true"
		}
		if x == 0 {
			return "false"
		}
		return fmt.Sprint(x)
	default:
		return strings.ToLower(strings.TrimSpace(fmt.Sprint(x)))
	}
}
