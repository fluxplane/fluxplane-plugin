package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// buildInvokeInput assembles an operation's input from three sources, in order:
//   - a base object from --input (or "-" to read stdin) / --input-file
//   - --arg key=value overrides, where a dotted key (a.b.c) sets a nested field
//     and the value is parsed as JSON when valid (numbers, bools, arrays,
//     objects, quoted strings) else treated as a plain string
//
// With no --arg, the base payload is returned untouched (so non-object inputs
// like arrays still work). With --arg present, the base must be a JSON object.
func buildInvokeInput(input, inputFile string, args []string, stdin io.Reader) (json.RawMessage, error) {
	var base json.RawMessage
	if strings.TrimSpace(input) == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("fluxplane-plugin: read stdin: %w", err)
		}
		base = json.RawMessage(bytes.TrimSpace(data))
		if len(base) > 0 && !json.Valid(base) {
			return nil, fmt.Errorf("fluxplane-plugin: stdin is not valid JSON")
		}
	} else {
		payload, err := readJSONPayload(input, inputFile)
		if err != nil {
			return nil, err
		}
		base = payload
	}

	if len(args) == 0 {
		return base, nil
	}

	obj := map[string]any{}
	if len(bytes.TrimSpace(base)) > 0 {
		if err := json.Unmarshal(base, &obj); err != nil {
			return nil, fmt.Errorf("fluxplane-plugin: --arg requires the base input to be a JSON object")
		}
	}
	for _, arg := range args {
		key, raw, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, fmt.Errorf("fluxplane-plugin: --arg %q must be key=value", arg)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("fluxplane-plugin: --arg %q has an empty key", arg)
		}
		setNestedValue(obj, strings.Split(key, "."), parseArgValue(raw))
	}
	return json.Marshal(obj)
}

// parseArgValue interprets a --arg value as JSON when it parses cleanly, so
// `limit=5` is a number, `all=true` a bool, `labels=["a"]` an array, and
// `summary=hello world` falls back to the literal string.
func parseArgValue(raw string) any {
	if raw != "" && json.Valid([]byte(raw)) {
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err == nil {
			return v
		}
	}
	return raw
}

// setNestedValue walks/creates nested objects along path and sets the leaf.
func setNestedValue(obj map[string]any, path []string, value any) {
	for i := 0; i < len(path)-1; i++ {
		key := path[i]
		child, ok := obj[key].(map[string]any)
		if !ok {
			child = map[string]any{}
			obj[key] = child
		}
		obj = child
	}
	obj[path[len(path)-1]] = value
}
