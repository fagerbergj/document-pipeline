package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/google/uuid"
)

// fullPipeline mirrors the standard stage order so artifactStageIndex and the
// field→stage fallback resolve every output field used in these tests.
func fullPipeline() model.PipelineConfig {
	return model.PipelineConfig{
		Stages: []model.StageDefinition{
			{Name: model.StageNameTranscribe, Outputs: []model.StageOutput{{Field: model.FieldRawText}}},
			{Name: model.StageNameOCR, Outputs: []model.StageOutput{{Field: model.FieldRawText}}},
			{Name: model.StageNameSummarize, Outputs: []model.StageOutput{{Field: model.FieldNarrativeSummary}}},
			{Name: model.StageNameClarify, Outputs: []model.StageOutput{{Field: model.FieldClarifiedText}}},
			{Name: model.StageNameClassify, Outputs: []model.StageOutput{{Field: model.FieldSummary}, {Field: model.FieldTags}}},
			{Name: model.StageNameEmbed},
		},
	}
}

func filenamesOf(d []model.Artifact) []string {
	out := make([]string, len(d))
	for i, a := range d {
		out[i] = a.Filename
	}
	return out
}

// seedArtifact inserts a derived (job-created) artifact for docID. When tagged
// is true the Stage/Field columns are populated as the worker now does;
// otherwise they stay nil to model legacy rows.
func seedDerived(t *testing.T, h *handler, docID, filename, stage, field string, tagged bool, at time.Time) model.Artifact {
	t.Helper()
	jobID := "job-" + stage
	a := model.Artifact{
		ID:           uuid.NewString(),
		DocumentID:   docID,
		Filename:     filename,
		ContentType:  "text/markdown",
		CreatedJobID: &jobID,
		CreatedAt:    at,
		UpdatedAt:    at,
	}
	if tagged {
		s, f := stage, field
		a.Stage, a.Field = &s, &f
	}
	if err := h.artifacts.Insert(context.Background(), a); err != nil {
		t.Fatalf("insert artifact %s: %v", filename, err)
	}
	return a
}

func buildDetail(t *testing.T, h *handler, docID string) []model.Artifact {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	detail, err := h.buildDocDetail(req, model.Document{ID: docID})
	if err != nil {
		t.Fatalf("buildDocDetail: %v", err)
	}
	if detail.Artifacts == nil {
		return nil
	}
	out := make([]model.Artifact, 0, len(*detail.Artifacts))
	for _, a := range *detail.Artifacts {
		art := model.Artifact{ID: a.Id.String(), Filename: a.Filename, CreatedJobID: nil}
		if a.IsCanonical != nil && *a.IsCanonical {
			// re-tag with a marker field so callers can find the canonical one
			c := "canonical"
			art.Field = &c
		}
		out = append(out, art)
	}
	return out
}

func TestBuildDocDetail_OrdersByStageDeterministically(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.pipeline = fullPipeline()
	docID := "doc-order"
	base := time.Now().UTC()

	// One source upload plus derived outputs inserted out of stage order.
	src := model.Artifact{ID: "src", DocumentID: docID, Filename: "photo.png", ContentType: "image/png", CreatedAt: base, UpdatedAt: base}
	if err := h.artifacts.Insert(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	seedDerived(t, h, docID, "clarified_text.md", model.StageNameClarify, model.FieldClarifiedText, true, base.Add(3*time.Second))
	seedDerived(t, h, docID, "raw_text.md", model.StageNameOCR, model.FieldRawText, true, base.Add(1*time.Second))
	seedDerived(t, h, docID, "summary.json", model.StageNameClassify, model.FieldSummary, true, base.Add(4*time.Second))
	seedDerived(t, h, docID, "narrative_summary.md", model.StageNameSummarize, model.FieldNarrativeSummary, true, base.Add(2*time.Second))

	want := []string{"photo.png", "raw_text.md", "narrative_summary.md", "clarified_text.md", "summary.json"}

	// Repeat to defeat the previous Go-map-iteration randomization regression:
	// the order must be identical every call.
	for i := 0; i < 25; i++ {
		got := filenamesOf(buildDetail(t, h, docID))
		if len(got) != len(want) {
			t.Fatalf("iter %d: got %v, want %v", i, got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("iter %d: order %v, want %v", i, got, want)
			}
		}
	}
}

func TestBuildDocDetail_LegacyUntaggedFallback(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.pipeline = fullPipeline()
	docID := "doc-legacy"
	base := time.Now().UTC()

	// Legacy rows: Stage/Field nil, resolved purely from the filename.
	seedDerived(t, h, docID, "clarified_text.md", "", "", false, base.Add(3*time.Second))
	seedDerived(t, h, docID, "raw_text.md", "", "", false, base.Add(1*time.Second))
	seedDerived(t, h, docID, "narrative_summary.md", "", "", false, base.Add(2*time.Second))

	want := []string{"raw_text.md", "narrative_summary.md", "clarified_text.md"}
	got := filenamesOf(buildDetail(t, h, docID))
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

func TestBuildDocDetail_CanonicalIsClarifiedText(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.pipeline = fullPipeline()
	docID := "doc-canon"
	base := time.Now().UTC()
	seedDerived(t, h, docID, "raw_text.md", model.StageNameOCR, model.FieldRawText, true, base.Add(1*time.Second))
	clar := seedDerived(t, h, docID, "clarified_text.md", model.StageNameClarify, model.FieldClarifiedText, true, base.Add(2*time.Second))
	seedDerived(t, h, docID, "summary.json", model.StageNameClassify, model.FieldSummary, true, base.Add(3*time.Second))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	detail, err := h.buildDocDetail(req, model.Document{ID: docID})
	if err != nil {
		t.Fatal(err)
	}
	var canonicalCount int
	var canonicalID string
	for _, a := range *detail.Artifacts {
		if a.IsCanonical != nil && *a.IsCanonical {
			canonicalCount++
			canonicalID = a.Id.String()
		}
	}
	if canonicalCount != 1 {
		t.Fatalf("want exactly 1 canonical artifact, got %d", canonicalCount)
	}
	if canonicalID != clar.ID {
		t.Fatalf("canonical id = %s, want clarified_text artifact %s", canonicalID, clar.ID)
	}
}

func TestBuildDocDetail_NoCanonicalWhenClarifySkipped(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.pipeline = fullPipeline()
	docID := "doc-noclar"
	base := time.Now().UTC()
	seedDerived(t, h, docID, "raw_text.md", model.StageNameOCR, model.FieldRawText, true, base.Add(1*time.Second))
	seedDerived(t, h, docID, "narrative_summary.md", model.StageNameSummarize, model.FieldNarrativeSummary, true, base.Add(2*time.Second))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	detail, err := h.buildDocDetail(req, model.Document{ID: docID})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range *detail.Artifacts {
		if a.IsCanonical != nil && *a.IsCanonical {
			t.Fatalf("no artifact should be canonical when clarify is absent, but %s is", a.Filename)
		}
	}
}

// A stage output and a downstream stage's untagged input draft share a
// filename. The tagged output must win the latest-per-filename dedup even
// though the input draft was written later.
func TestBuildDocDetail_PrefersTaggedOutputOverUntaggedInput(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.pipeline = fullPipeline()
	docID := "doc-dedup"
	base := time.Now().UTC()

	output := seedDerived(t, h, docID, "narrative_summary.md", model.StageNameSummarize, model.FieldNarrativeSummary, true, base.Add(1*time.Second))
	// clarify's input copy of narrative_summary, written later, untagged.
	seedDerived(t, h, docID, "narrative_summary.md", "", "", false, base.Add(5*time.Second))

	got := buildDetail(t, h, docID)
	if len(got) != 1 {
		t.Fatalf("want 1 deduped artifact, got %d (%v)", len(got), filenamesOf(got))
	}
	if got[0].ID != output.ID {
		t.Fatalf("dedup kept %s, want tagged output %s", got[0].ID, output.ID)
	}
}
