package cli

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/fluxplane/fluxplane-plugin/management"
)

// extractPath walks a decoded JSON value (map[string]any / []any / scalar)
// following dot-separated segments. A numeric segment indexes into an array
// (e.g. "items.0.key"). Returns (value, true) on a clean hit, (nil, false) if any
// segment misses. Keys containing a literal dot are not addressable.
func extractPath(v any, path string) (any, bool) {
	cur := v
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// printOperationResultStrict renders an invoke result honoring the output flags
// and reports whether any requested --field path was missing:
//   - default: the full {plugin, instance, operation, result} envelope
//   - resultOnly: just the result payload
//   - fieldPaths: extract specific dot-paths from the result. A single path
//     prints its value; multiple paths print an object keyed by path. Any missing
//     paths are recorded under "missing" and make the returned bool true.
func printOperationResultStrict(w io.Writer, res management.OperationInvokeResult, resultOnly bool, fieldPaths []string) (bool, error) {
	if len(fieldPaths) == 0 && !resultOnly {
		return false, printJSON(w, res)
	}

	var decoded any
	if len(res.Result) > 0 {
		if err := json.Unmarshal(res.Result, &decoded); err != nil {
			// Result isn't JSON we can walk — fall back to printing it raw.
			return false, printJSON(w, json.RawMessage(res.Result))
		}
	}

	if len(fieldPaths) == 0 {
		return false, printJSON(w, decoded)
	}

	if len(fieldPaths) == 1 {
		value, ok := extractPath(decoded, fieldPaths[0])
		if !ok {
			return true, printJSON(w, map[string]any{"missing": fieldPaths})
		}
		return false, printJSON(w, value)
	}

	out := map[string]any{}
	var missing []string
	for _, p := range fieldPaths {
		if value, ok := extractPath(decoded, p); ok {
			out[p] = value
		} else {
			out[p] = nil
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		out["missing"] = missing
	}
	return len(missing) > 0, printJSON(w, out)
}

// secretKeyFragments name input fields whose values must be redacted when an
// input payload is echoed back (e.g. dry-run). Chosen to avoid false positives
// like issue_key / parent_key / author / account_id.
var secretKeyFragments = []string{
	"password", "passwd", "secret", "token", "credential",
	"api_key", "apikey", "access_key", "private_key", "client_secret", "authorization",
}

func isSecretKey(key string) bool {
	key = strings.ToLower(key)
	for _, fragment := range secretKeyFragments {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

// redactInputForDisplay returns a copy of an input payload with secret-ish field
// values masked, so echoing input (dry-run, validation reports) never leaks
// tokens into logs.
func redactInputForDisplay(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	redactSecrets(v)
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func redactSecrets(v any) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			if isSecretKey(key) {
				node[key] = "[redacted]"
				continue
			}
			redactSecrets(child)
		}
	case []any:
		for _, item := range node {
			redactSecrets(item)
		}
	}
}

// splitFieldPaths parses a comma-separated --field value into trimmed paths.
func splitFieldPaths(value string) []string {
	var out []string
	for _, p := range strings.Split(value, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
