package core

import (
	"testing"

	"github.com/fagerbergj/document-pipeline/server/core/model"
)

func TestCheckContinueIf(t *testing.T) {
	stage := model.StageDefinition{
		ContinueIf: []map[string]any{{"confidence": "high"}},
	}

	if checkContinueIf(stage, model.ConfidenceHigh) != true {
		t.Error("high confidence should satisfy continue_if: high")
	}
	if checkContinueIf(stage, model.ConfidenceMedium) != false {
		t.Error("medium confidence should not satisfy continue_if: high")
	}
	if checkContinueIf(stage, model.ConfidenceLow) != false {
		t.Error("low confidence should not satisfy continue_if: high")
	}
}

func TestCheckContinueIf_NilRules(t *testing.T) {
	stage := model.StageDefinition{}
	if !checkContinueIf(stage, model.ConfidenceLow) {
		t.Error("nil continue_if should always pass")
	}
}

func TestCheckStartIf_RequireContext(t *testing.T) {
	stage := model.StageDefinition{RequireContext: true}

	docWithContext := model.Document{AdditionalContext: "some context"}
	if !checkStartIf(docWithContext, stage) {
		t.Error("doc with context should pass start_if when require_context=true")
	}

	docWithLinked := model.Document{LinkedContexts: []string{"ctx-id"}}
	if !checkStartIf(docWithLinked, stage) {
		t.Error("doc with linked contexts should pass start_if")
	}

	docNoContext := model.Document{}
	if checkStartIf(docNoContext, stage) {
		t.Error("doc without context should fail start_if when require_context=true")
	}
}

func TestIsSkipFileType(t *testing.T) {
	stage := model.StageDefinition{
		SkipIf: map[string]any{"file_type": []any{"txt", "md"}},
	}

	if !isSkipFileType(stage, model.FileTypeTXT) {
		t.Error("txt should be skipped")
	}
	if !isSkipFileType(stage, model.FileTypeMD) {
		t.Error("md should be skipped")
	}
	if isSkipFileType(stage, model.FileTypePNG) {
		t.Error("png should not be skipped")
	}
}

func TestParseLLMResponse_JSON(t *testing.T) {
	raw := `{"tags": ["note", "meeting"], "summary": "A meeting note.", "confidence": "high"}`
	stage := model.StageDefinition{
		Outputs: []model.StageOutput{
			{Field: "tags", Type: "json_array"},
			{Field: "summary", Type: "text"},
		},
	}

	_, outputs, confidence, _, _ := parseLLMResponse(raw, "", "", stage)

	if confidence != model.ConfidenceHigh {
		t.Errorf("confidence: got %q, want %q", confidence, model.ConfidenceHigh)
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs: got %d, want 2", len(outputs))
	}
}

func TestParseLLMResponse_ClarifiedText(t *testing.T) {
	raw := `<clarified_text>
Hello world
</clarified_text>
<confidence>medium</confidence>
<questions>[]</questions>
<document_context_update>Updated context</document_context_update>`

	stage := model.StageDefinition{Output: "clarified_text"}
	_, outputs, confidence, questions, suggestions := parseLLMResponse(raw, "ocr_raw", "raw input", stage)

	if confidence != model.ConfidenceMedium {
		t.Errorf("confidence: got %q, want %q", confidence, model.ConfidenceMedium)
	}
	if len(outputs) != 1 || outputs[0].Text != "Hello world" {
		t.Errorf("outputs: got %+v", outputs)
	}
	if len(questions) != 0 {
		t.Errorf("questions: got %d, want 0", len(questions))
	}
	if suggestions.AdditionalContext != "Updated context" {
		t.Errorf("suggestions.AdditionalContext: got %q", suggestions.AdditionalContext)
	}
}
