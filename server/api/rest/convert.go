package rest

import (
	"github.com/fagerbergj/document-pipeline/server/api/schema"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/google/uuid"
)

// ── UUID helpers ──────────────────────────────────────────────────────────────

func toUUID(s string) openapi_types.UUID {
	u, _ := uuid.Parse(s)
	return openapi_types.UUID(u)
}

func toUUIDPtr(s *string) *openapi_types.UUID {
	if s == nil {
		return nil
	}
	u := toUUID(*s)
	return &u
}

func toUUIDSlice(ss []string) *[]openapi_types.UUID {
	out := make([]openapi_types.UUID, 0, len(ss))
	for _, s := range ss {
		out = append(out, toUUID(s))
	}
	return &out
}

// ── scalar helpers ────────────────────────────────────────────────────────────

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func boolPtr(b bool) *bool { return &b }

func intPtr(i int) *int { return &i }

func float32Ptr(f float64) *float32 {
	v := float32(f)
	return &v
}

func answerPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ── Jobs ──────────────────────────────────────────────────────────────────────

func toJobSummary(job model.Job, title *string) schema.JobSummary {
	return schema.JobSummary{
		Id:         toUUID(job.ID),
		DocumentId: toUUID(job.DocumentID),
		Title:      title,
		Stage:      job.Stage,
		Status:     schema.JobStatus(job.Status),
		CreatedAt:  job.CreatedAt,
		UpdatedAt:  job.UpdatedAt,
	}
}

func toJobDetail(job model.Job, title *string) schema.JobDetail {
	opts := toJobOptions(job.Options)
	runs := toRuns(job.Runs)
	return schema.JobDetail{
		Id:         toUUID(job.ID),
		DocumentId: toUUID(job.DocumentID),
		Title:      title,
		Stage:      job.Stage,
		Status:     schema.JobStatus(job.Status),
		Options:    &opts,
		Runs:       &runs,
		CreatedAt:  job.CreatedAt,
		UpdatedAt:  job.UpdatedAt,
	}
}

func toJobOptions(o model.JobOptions) schema.JobOptions {
	opts := schema.JobOptions{
		RequireContext: boolPtr(o.RequireContext),
	}
	if o.Embed != nil {
		opts.Embed = &schema.EmbedOptions{
			EmbedImage: boolPtr(o.Embed.EmbedImage),
		}
	}
	return opts
}

func toRuns(runs []model.Run) []schema.Run {
	out := make([]schema.Run, 0, len(runs))
	for _, r := range runs {
		out = append(out, toRun(r))
	}
	return out
}

func toRun(r model.Run) schema.Run {
	return schema.Run{
		Id:          toUUID(r.ID),
		Inputs:      toIOFields(r.Inputs),
		Outputs:     toIOFields(r.Outputs),
		Confidence:  schema.RunConfidence(r.Confidence),
		Questions:   toQuestions(r.Questions),
		Suggestions: toSuggestions(r.Suggestions),
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

func toIOFields(fields []model.Field) []schema.RunIOField {
	out := make([]schema.RunIOField, 0, len(fields))
	for _, f := range fields {
		name := f.Field
		out = append(out, schema.RunIOField{
			Field: &name,
			Text:  f.Text,
		})
	}
	return out
}

func toQuestions(qs []model.Question) []schema.RunQuestion {
	out := make([]schema.RunQuestion, 0, len(qs))
	for _, q := range qs {
		out = append(out, schema.RunQuestion{
			Segment:  q.Segment,
			Question: q.Question,
			Answer:   answerPtr(q.Answer),
		})
	}
	return out
}

func toSuggestions(s model.Suggestions) schema.RunSuggestions {
	return schema.RunSuggestions{
		AdditionalContext: strPtr(s.AdditionalContext),
		LinkedContext:     strPtr(s.LinkedContext),
		LinkedContextId:   toUUIDPtr(s.LinkedContextID),
	}
}

// ── Documents ─────────────────────────────────────────────────────────────────

func toDocSummary(doc model.Document, currentJob *model.Job) schema.DocumentSummary {
	var currentJobID *openapi_types.UUID
	if currentJob != nil {
		id := toUUID(currentJob.ID)
		currentJobID = &id
	}
	return schema.DocumentSummary{
		Id:           toUUID(doc.ID),
		Title:        doc.Title,
		CurrentJobId: currentJobID,
		CreatedAt:    doc.CreatedAt,
		UpdatedAt:    doc.UpdatedAt,
	}
}

func toDocDetail(doc model.Document, currentJob *model.Job, artifacts []model.Artifact) schema.DocumentDetail {
	var currentJobID *openapi_types.UUID
	if currentJob != nil {
		id := toUUID(currentJob.ID)
		currentJobID = &id
	}
	arts := toArtifacts(artifacts)
	return schema.DocumentDetail{
		Id:                toUUID(doc.ID),
		Title:             doc.Title,
		CurrentJobId:      currentJobID,
		AdditionalContext: &doc.AdditionalContext,
		LinkedContexts:    toUUIDSlice(doc.LinkedContexts),
		Artifacts:         &arts,
		CreatedAt:         doc.CreatedAt,
		UpdatedAt:         doc.UpdatedAt,
	}
}

func toArtifacts(arts []model.Artifact) []schema.Artifact {
	out := make([]schema.Artifact, 0, len(arts))
	for _, a := range arts {
		out = append(out, schema.Artifact{
			Id:           toUUID(a.ID),
			Filename:     a.Filename,
			ContentType:  a.ContentType,
			CreatedJobId: toUUIDPtr(a.CreatedJobID),
			CreatedAt:    a.CreatedAt,
			UpdatedAt:    a.UpdatedAt,
		})
	}
	return out
}

// ── Contexts ──────────────────────────────────────────────────────────────────

func toContextEntry(e model.Context) schema.ContextEntry {
	return schema.ContextEntry{
		Id:   toUUID(e.ID),
		Name: e.Name,
		Text: e.Text,
	}
}

// ── Chats ─────────────────────────────────────────────────────────────────────

func toRagRetrieval(r model.RAGConfig) schema.RagRetrieval {
	return schema.RagRetrieval{
		Enabled:      boolPtr(r.Enabled),
		MaxSources:   intPtr(r.MaxSources),
		MinimumScore: float32Ptr(r.MinimumScore),
	}
}

func toChatSummary(c model.ChatSession) schema.ChatSummary {
	return schema.ChatSummary{
		Id:           c.ID,
		Title:        strPtr(c.Title),
		SystemPrompt: strPtr(c.SystemPrompt),
		RagRetrieval: toRagRetrieval(c.RAGRetrieval),
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
	}
}

func toChatDetail(c model.ChatSession, msgs []model.ChatMessage) schema.ChatDetail {
	messages := toChatMessages(msgs)
	return schema.ChatDetail{
		Id:           c.ID,
		Title:        strPtr(c.Title),
		SystemPrompt: strPtr(c.SystemPrompt),
		RagRetrieval: toRagRetrieval(c.RAGRetrieval),
		Messages:     &messages,
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
	}
}

func toChatMessages(msgs []model.ChatMessage) []schema.ChatMessage {
	out := make([]schema.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toChatMessage(m))
	}
	return out
}

func toChatMessage(m model.ChatMessage) schema.ChatMessage {
	sources := toSourceDocs(m.Sources)
	return schema.ChatMessage{
		Id:         toUUID(m.ID),
		ExternalId: m.ExternalID,
		Role:       schema.ChatMessageRole(m.Role),
		Content:    m.Content,
		Sources:    &sources,
		CreatedAt:  m.CreatedAt,
	}
}

func toSourceDocs(refs []model.SourceRef) []schema.SourceDoc {
	out := make([]schema.SourceDoc, 0, len(refs))
	for _, r := range refs {
		out = append(out, schema.SourceDoc{
			DocumentId: toUUID(r.DocumentID),
			Title:      r.Title,
			Summary:    strPtr(r.Summary),
			DateMonth:  strPtr(r.DateMonth),
			Score:      float32(r.Score),
		})
	}
	return out
}

// ── Pipelines ─────────────────────────────────────────────────────────────────

func toPipeline(cfg model.PipelineConfig) schema.Pipeline {
	stages := make([]schema.StageSummary, 0, len(cfg.Stages))
	for _, s := range cfg.Stages {
		stages = append(stages, schema.StageSummary{
			Name:  s.Name,
			Type:  s.Type,
			Model: strPtr(s.Model),
		})
	}
	return schema.Pipeline{
		Id:     "pipeline",
		Name:   "pipeline",
		Stages: stages,
	}
}

func toPipelineDetail(cfg model.PipelineConfig) schema.PipelineDetail {
	stages := make([]schema.StageDetail, 0, len(cfg.Stages))
	for _, s := range cfg.Stages {
		stages = append(stages, toStageDetail(s))
	}
	return schema.PipelineDetail{
		Id:     "pipeline",
		Name:   "pipeline",
		Stages: stages,
	}
}

func toStageDetail(s model.StageDefinition) schema.StageDetail {
	d := schema.StageDetail{
		Name:  s.Name,
		Type:  s.Type,
		Model: strPtr(s.Model),
	}

	var inputs []string
	if s.Input != "" {
		inputs = []string{s.Input}
	}
	if len(inputs) > 0 {
		d.Inputs = &inputs
	}

	type outEntry struct {
		Field string `json:"field"`
		Type  string `json:"type"`
	}
	var outputs []struct {
		Field string `json:"field"`
		Type  string `json:"type"`
	}
	for _, o := range s.Outputs {
		outputs = append(outputs, struct {
			Field string `json:"field"`
			Type  string `json:"type"`
		}{Field: o.Field, Type: o.Type})
	}
	if s.Output != "" && len(s.Outputs) == 0 {
		outputs = append(outputs, struct {
			Field string `json:"field"`
			Type  string `json:"type"`
		}{Field: s.Output, Type: "text"})
	}
	if len(outputs) > 0 {
		d.Outputs = &outputs
	}

	if s.SkipIf != nil {
		d.SkipIf = &s.SkipIf
	}
	if s.StartIf != nil {
		d.StartIf = &s.StartIf
	}
	if s.ContinueIf != nil {
		d.ContinueIf = &s.ContinueIf
	}

	return d
}
