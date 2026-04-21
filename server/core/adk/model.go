package adk

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/genai"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// PortLLMModel implements model.LLM backed by port.LLMInference, enabling the
// ADK agent loop to work with any backend including test mocks.
type PortLLMModel struct {
	llm  port.LLMInference
	name string
}

// NewPortLLMModel returns a model.LLM that delegates to llm using modelName.
func NewPortLLMModel(llm port.LLMInference, modelName string) *PortLLMModel {
	return &PortLLMModel{llm: llm, name: modelName}
}

func (m *PortLLMModel) Name() string { return m.name }

// GenerateContent fulfils model.LLM. stream is ignored; ChatWithTools is always
// non-streaming.
func (m *PortLLMModel) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := m.call(ctx, req)
		yield(resp, err)
	}
}

func (m *PortLLMModel) call(ctx context.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
	messages := m.buildMessages(req)
	tools := m.buildTools(req)

	text, calls, err := m.llm.ChatWithTools(ctx, m.name, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("adk model call: %w", err)
	}

	var parts []*genai.Part
	if len(calls) > 0 {
		for _, c := range calls {
			parts = append(parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   c.ID,
					Name: c.Name,
					Args: c.Arguments,
				},
			})
		}
	} else {
		parts = append(parts, &genai.Part{Text: text})
	}

	return &model.LLMResponse{
		Content:      &genai.Content{Role: "model", Parts: parts},
		TurnComplete: true,
	}, nil
}

func (m *PortLLMModel) buildMessages(req *model.LLMRequest) []port.LLMMessage {
	var msgs []port.LLMMessage

	if req.Config != nil && req.Config.SystemInstruction != nil {
		if txt := partsText(req.Config.SystemInstruction.Parts); txt != "" {
			msgs = append(msgs, port.LLMMessage{Role: "system", Content: txt})
		}
	}

	for _, c := range req.Contents {
		role := c.Role
		if role == "model" {
			role = "assistant"
		}

		msg := port.LLMMessage{Role: role}

		for _, p := range c.Parts {
			switch {
			case p.Text != "":
				msg.Content += p.Text

			case p.InlineData != nil:
				// genai.Part.InlineData.Data holds raw bytes (not base64).
				msg.Images = append(msg.Images, p.InlineData.Data)

			case p.FunctionCall != nil:
				msg.ToolCalls = append(msg.ToolCalls, port.LLMToolCall{
					ID:        p.FunctionCall.ID,
					Name:      p.FunctionCall.Name,
					Arguments: p.FunctionCall.Args,
				})

			case p.FunctionResponse != nil:
				// Flush the current message before appending the tool response.
				if msg.Role != "" {
					msgs = append(msgs, msg)
					msg = port.LLMMessage{}
				}
				msgs = append(msgs, port.LLMMessage{
					Role:       "tool",
					Content:    mapToJSON(p.FunctionResponse.Response),
					ToolCallID: p.FunctionResponse.ID,
				})
			}
		}

		if msg.Role != "" {
			msgs = append(msgs, msg)
		}
	}

	return msgs
}

func (m *PortLLMModel) buildTools(req *model.LLMRequest) []port.LLMTool {
	if req.Config == nil {
		return nil
	}
	var tools []port.LLMTool
	for _, t := range req.Config.Tools {
		for _, fd := range t.FunctionDeclarations {
			tools = append(tools, port.LLMTool{
				Name:        fd.Name,
				Description: fd.Description,
				Parameters:  schemaToMap(fd.Parameters),
			})
		}
	}
	return tools
}

func partsText(parts []*genai.Part) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func schemaToMap(s *genai.Schema) map[string]any {
	if s == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	m := map[string]any{"type": strings.ToLower(string(s.Type))}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = schemaToMap(v)
		}
		m["properties"] = props
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if s.Items != nil {
		m["items"] = schemaToMap(s.Items)
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	return m
}

func mapToJSON(m map[string]any) string {
	if m == nil {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}
