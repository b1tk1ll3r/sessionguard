package windowsx

import (
	"encoding/json"
	"fmt"
	"strings"
)

// decodePowerShellJSON extracts the first JSON value from PowerShell stdout.
// Windows PowerShell can occasionally surround redirected output with startup,
// progress, or CLIXML noise. The command runner keeps stderr separate, but this
// parser is deliberately defensive so a valid JSON payload is not discarded
// merely because a host emits an unexpected banner before/after it.
func decodePowerShellJSON(raw string, out any) error {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if raw == "" {
		raw = "[]"
	}

	payload, err := firstJSONValue(raw)
	if err != nil {
		return fmt.Errorf("no JSON value in PowerShell output: %w (output=%q)", err, raw)
	}

	payload = strings.TrimSpace(payload)
	// Windows PowerShell/ConvertTo-Json may return a single object when only one
	// item exists. RemoteApp callers always decode into a slice, so normalize it.
	if strings.HasPrefix(payload, "{") {
		payload = "[" + payload + "]"
	}
	if err := json.Unmarshal([]byte(payload), out); err != nil {
		return fmt.Errorf("decode PowerShell JSON: %w (json=%q, output=%q)", err, payload, raw)
	}
	return nil
}

func firstJSONValue(raw string) (string, error) {
	var lastErr error
	for i := 0; i < len(raw); i++ {
		if raw[i] != '[' && raw[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(raw[i:]))
		var msg json.RawMessage
		if err := dec.Decode(&msg); err != nil {
			lastErr = err
			continue
		}
		return string(msg), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("missing JSON object/array delimiter")
	}
	return "", lastErr
}
