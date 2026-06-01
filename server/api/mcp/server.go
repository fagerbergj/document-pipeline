package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fagerbergj/document-pipeline/server/core/adk/tools"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

const (
	MCPServerName         = "my-documents"
	MCPServerInstructions = "Access your personal document knowledge base"
)

// Server wraps the MCP server and exposes run* functions from adk/tools.
type Server struct {
	server *mcp.Server
}

// ToolDefinition represents a tool definition from mcp.json
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPConfig is the top-level configuration loaded from mcp.json
type MCPConfig struct {
	Server struct {
		Name         string `json:"name"`
		Version      string `json:"version"`
		Instructions string `json:"instructions"`
	} `json:"mcpServer"`
	Tools []ToolDefinition `json:"tools"`
}

// New creates a new MCP server instance with the configured tools.
// It reads tool definitions from mcp.json if the file exists, otherwise uses defaults.
func New(
	store port.EmbedStore,
	indexer port.DocumentIndexer,
	embedFn tools.EmbedFn,
	embedModel string,
	getDoc tools.DocLookupFn,
	getDocs tools.DocsBatchFn,
	stageData tools.StageDataFn,
	stageDataBatch tools.StageDataBatchFn,
	maxSources int,
	minScore float64,
	maxResults int,
) (*Server, error) {
	if maxSources <= 0 {
		maxSources = 5
	}
	if maxResults <= 0 {
		maxResults = 10
	}

	// Try to load tool definitions from mcp.json
	toolsDef, err := loadToolDefinitions()
	if err != nil {
		return nil, fmt.Errorf("failed to load tool definitions: %w", err)
	}

	// Configure server based on loaded config
	serverName := MCPServerName
	serverVersion := "1.0.0"
	serverInstructions := MCPServerInstructions

	if toolsDef != nil {
		serverName = toolsDef.Server.Name
		if toolsDef.Server.Version != "" {
			serverVersion = toolsDef.Server.Version
		}
		if toolsDef.Server.Instructions != "" {
			serverInstructions = toolsDef.Server.Instructions
		}
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})

	s := &Server{server: srv}

	// Add tools based on loaded definitions
	if toolsDef != nil {
		if err := s.addToolsFromConfig(toolsDef, store, embedFn, embedModel, maxSources, minScore, indexer, getDocs, stageDataBatch, maxResults, getDoc, stageData); err != nil {
			return nil, err
		}
	} else {
		// Fallback to hardcoded definitions if mcp.json not found
		return nil, fmt.Errorf("mcp.json not found - this should never happen with //go:embed")
	}

	return s, nil
}

//go:embed mcp.json
var mcpJSONData []byte

// loadToolDefinitions reads and parses the mcp.json file
func loadToolDefinitions() (*MCPConfig, error) {
	if len(mcpJSONData) == 0 {
		return nil, nil
	}

	var config MCPConfig
	if err := json.Unmarshal(mcpJSONData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse mcp.json: %w", err)
	}

	return &config, nil
}

// addToolsFromConfig adds tools using the schema from mcp.json.
// search_documents is only registered when indexer != nil.
func (s *Server) addToolsFromConfig(config *MCPConfig, store port.EmbedStore, embedFn tools.EmbedFn, embedModel string, maxSources int, minScore float64, indexer port.DocumentIndexer, getDocs tools.DocsBatchFn, stageDataBatch tools.StageDataBatchFn, maxResults int, getDoc tools.DocLookupFn, stageData tools.StageDataFn) error {
	for _, toolDef := range config.Tools {
		var handler mcp.ToolHandler

		// Skip search_documents if indexer is nil (OpenSearch unavailable)
		if toolDef.Name == "search_documents" && indexer == nil {
			continue
		}

		switch toolDef.Name {
		case "rag_search":
			handler = makeRagSearchHandler(store, embedFn, embedModel, maxSources, minScore)
		case "search_documents":
			handler = makeSearchDocumentsHandler(indexer, getDocs, stageDataBatch, maxResults)
		case "get_document":
			handler = makeGetDocumentHandler(getDoc, stageData)
		default:
			continue
		}

		s.server.AddTool(&mcp.Tool{
			Name:        toolDef.Name,
			Description: toolDef.Description,
			InputSchema: toolDef.InputSchema,
		}, handler)
	}

	return nil
}

func makeRagSearchHandler(store port.EmbedStore, embedFn tools.EmbedFn, embedModel string, maxSources int, minScore float64) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args tools.RagSearchArgs
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Invalid arguments: %v", err)}},
			}, nil
		}

		// Read k and min_score from request args (fallback to defaults if not provided)
		var argsMap map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &argsMap); err != nil {
			argsMap = nil
		}

		k := maxSources
		if argsMap != nil {
			if v, ok := argsMap["k"]; ok {
				if n, ok := v.(float64); ok {
					if n > 0 {
						k = int(n)
					}
				}
			}
		}

		reqMinScore := minScore
		if argsMap != nil {
			if v, ok := argsMap["min_score"]; ok {
				if n, ok := v.(float64); ok {
					reqMinScore = n
				}
			}
		}

		result, err := tools.RunRagSearch(ctx, store, embedFn, embedModel, k, reqMinScore, args)
		if err != nil {
			result := &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Tool error: %v", err)}},
			}
			result.SetError(err)
			return result, nil
		}

		jsonBytes, err := json.Marshal(result)
		if err != nil {
			result := &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Result serialization error: %v", err)}},
			}
			result.SetError(err)
			return result, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
		}, nil
	}
}

func makeSearchDocumentsHandler(indexer port.DocumentIndexer, getDocs tools.DocsBatchFn, stageDataBatch tools.StageDataBatchFn, maxResults int) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if indexer == nil {
			r := &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "search_documents is disabled (no OpenSearch indexer available)"}},
			}
			r.SetError(fmt.Errorf("search_documents is disabled (no OpenSearch indexer available)"))
			return r, nil
		}

		var args tools.SearchDocumentsArgs
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Invalid arguments: %v", err)}},
			}, nil
		}

		k := maxResults
		var argsMap map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &argsMap); err == nil {
			if v, ok := argsMap["k"]; ok {
				if n, ok := v.(float64); ok {
					k = int(n)
				}
			}
		}

		result, err := tools.RunSearchDocuments(ctx, indexer, getDocs, stageDataBatch, k, args)
		if err != nil {
			r := &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Tool error: %v", err)}},
			}
			r.SetError(err)
			return r, nil
		}

		jsonBytes, err := json.Marshal(result)
		if err != nil {
			r := &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Result serialization error: %v", err)}},
			}
			r.SetError(err)
			return r, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
		}, nil
	}
}

func makeGetDocumentHandler(getDoc tools.DocLookupFn, stageData tools.StageDataFn) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args tools.GetDocumentArgs
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Invalid arguments: %v", err)}},
			}, nil
		}

		result, err := tools.RunGetDocument(ctx, getDoc, stageData, args)
		if err != nil {
			r := &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Tool error: %v", err)}},
			}
			r.SetError(err)
			return r, nil
		}

		jsonBytes, err := json.Marshal(result)
		if err != nil {
			r := &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Result serialization error: %v", err)}},
			}
			r.SetError(err)
			return r, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
		}, nil
	}
}

// HTTPHandler returns an HTTP handler for the MCP server using Streamable HTTP transport.
func (s *Server) HTTPHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return s.server
	}, &mcp.StreamableHTTPOptions{
		JSONResponse: true,
	})
}

// AuthenticatedHandler wraps the MCP handler with API key auth if MCP_API_KEY is set.
func (s *Server) AuthenticatedHandler() http.Handler {
	apiKey := os.Getenv("MCP_API_KEY")
	// Build the handler once and reuse it for all requests to preserve session state
	baseHandler := s.HTTPHandler()
	if apiKey == "" {
		slog.Warn("MCP_API_KEY not set - MCP endpoint will be unauthenticated. Ensure this is internal-only.")
		return baseHandler
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		expected := "Bearer " + apiKey

		if auth != expected {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		baseHandler.ServeHTTP(w, r)
	})
}
