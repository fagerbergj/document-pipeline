// Package stagefield provides read helpers for the stage_name → field_name →
// value map produced by core.CollectStageData. Pulled into its own leaf
// package so both the indexer (in core) and chat tools (in core/adk/tools)
// can share an implementation without an import cycle.
package stagefield

import (
	"encoding/json"
	"strings"

	"github.com/fagerbergj/document-pipeline/server/core/model"
)

// String reads a string-valued output field from a specific stage's results.
// Returns "" when the stage, field, or value is absent or the value is not a
// string.
func String(sd model.StageOutputs, stage, field string) string {
	v, ok := sd[stage][field]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Tags reads a tag-list output field. CollectStageData stores classify's tags
// as the raw artifact contents (a JSON-encoded string), but test fixtures
// sometimes pass the pre-parsed shape — we accept []string, []any, and the
// production string form.
func Tags(sd model.StageOutputs, stage, field string) []string {
	v, ok := sd[stage][field]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		raw := strings.TrimSpace(t)
		if raw == "" {
			return nil
		}
		var tags []string
		if err := json.Unmarshal([]byte(raw), &tags); err != nil {
			return nil
		}
		return tags
	}
	return nil
}
