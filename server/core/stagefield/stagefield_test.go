package stagefield_test

import (
	"reflect"
	"testing"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/stagefield"
)

func TestString(t *testing.T) {
	sd := model.StageOutputs{
		"classify": {"summary": "abstract", "tags": `["a"]`},
	}
	if got := stagefield.String(sd, "classify", "summary"); got != "abstract" {
		t.Errorf("got %q", got)
	}
	if got := stagefield.String(sd, "classify", "missing"); got != "" {
		t.Errorf("missing field should return empty, got %q", got)
	}
	if got := stagefield.String(sd, "nostage", "summary"); got != "" {
		t.Errorf("missing stage should return empty, got %q", got)
	}
	// Non-string value returns empty.
	sd2 := model.StageOutputs{"x": {"v": 42}}
	if got := stagefield.String(sd2, "x", "v"); got != "" {
		t.Errorf("non-string value should return empty, got %q", got)
	}
}

func TestTags_AllShapes(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  []string
	}{
		{"json string (production)", `["a","b","c"]`, []string{"a", "b", "c"}},
		{"pre-parsed []string", []string{"x", "y"}, []string{"x", "y"}},
		{"pre-parsed []any", []any{"p", "q", 7}, []string{"p", "q"}}, // non-strings dropped
		{"empty string", "", nil},
		{"malformed json", `not-json`, nil},
		{"whitespace-only string", "   ", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sd := model.StageOutputs{"classify": {"tags": tc.value}}
			got := stagefield.Tags(sd, "classify", "tags")
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestTags_Missing(t *testing.T) {
	sd := model.StageOutputs{}
	if got := stagefield.Tags(sd, "classify", "tags"); got != nil {
		t.Errorf("missing stage should return nil, got %#v", got)
	}
}
