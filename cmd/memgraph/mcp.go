package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/prastuvwxyz/memgraph/internal/rank"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start an MCP stdio server exposing memgraph as AI tools",
	Long: `Start a Model Context Protocol (MCP) server over stdio.

Exposes three tools to any MCP-compatible AI assistant:
  memgraph_query   — BM25+vector search across the indexed vault
  memgraph_similar — find files semantically similar to a text snippet
  memgraph_stats   — return index statistics

Claude Code setup (~/.claude/settings.json):

  {
    "mcpServers": {
      "memgraph": {
        "command": "memgraph",
        "args": ["mcp", "--dir", "/path/to/your/vault"]
      }
    }
  }`,
	Args: cobra.NoArgs,
	RunE: runMCP,
}

// mcpRequest is a JSON-RPC 2.0 request.
type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// mcpResponse is a JSON-RPC 2.0 response.
type mcpResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *mcpError `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func runMCP(cmd *cobra.Command, args []string) error {
	dir := "."
	if dirFlag != "" {
		dir = dirFlag
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	workspace := config.LoadOrDefault(abs)
	dbPath := filepath.Join(workspace.Root, ".memgraph", "index.db")

	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var req mcpRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}

		var result any
		var mcpErr *mcpError

		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "memgraph", "version": "1.0.0"},
			}

		case "notifications/initialized":
			continue

		case "tools/list":
			result = map[string]any{
				"tools": []map[string]any{
					{
						"name":        "memgraph_query",
						"description": "Search the indexed markdown vault using BM25+vector hybrid search. Returns ranked results with scores, tags, and relevance reasons.",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"topic": map[string]any{"type": "string", "description": "Search query"},
								"top":   map[string]any{"type": "integer", "description": "Max results (default 5)"},
								"ns":    map[string]any{"type": "string", "description": "Namespace to search within (optional)"},
								"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter to files containing all these tags (optional)"},
							},
							"required": []string{"topic"},
						},
					},
					{
						"name":        "memgraph_similar",
						"description": "Find files in the vault that are semantically similar to a given text snippet. Useful for deduplication or checking if knowledge already exists.",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"text": map[string]any{"type": "string", "description": "Text to find similar content for"},
								"top":  map[string]any{"type": "integer", "description": "Max results (default 5)"},
							},
							"required": []string{"text"},
						},
					},
					{
						"name":        "memgraph_stats",
						"description": "Return index statistics: number of indexed files, index size, and last modified time.",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{},
						},
					},
				},
			}

		case "tools/call":
			result, mcpErr = handleToolCall(req.Params, dbPath, workspace)

		default:
			mcpErr = &mcpError{Code: -32601, Message: "method not found"}
		}

		resp := mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: mcpErr}
		_ = enc.Encode(resp)
	}

	return scanner.Err()
}

func handleToolCall(params json.RawMessage, dbPath string, workspace *config.Workspace) (any, *mcpError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &mcpError{Code: -32600, Message: "invalid params"}
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, &mcpError{Code: -32000, Message: "no index found — run: memgraph index ."}
	}

	db, err := index.Open(dbPath)
	if err != nil {
		return nil, &mcpError{Code: -32000, Message: fmt.Sprintf("open index: %v", err)}
	}
	defer db.Close()

	emb := resolveEmbedder(workspace.Config.Embed)

	switch p.Name {
	case "memgraph_query":
		var args struct {
			Topic string   `json:"topic"`
			Top   int      `json:"top"`
			NS    string   `json:"ns"`
			Tags  []string `json:"tags"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return nil, &mcpError{Code: -32600, Message: "invalid arguments"}
		}
		if args.Top <= 0 {
			args.Top = 5
		}
		var ns []string
		if args.NS != "" {
			ns = []string{args.NS}
		}
		results, err := rank.Search(db.SqlDB(), args.Topic, rank.SearchOpts{
			TopN:       args.Top,
			Namespaces: ns,
			Tags:       args.Tags,
			Embedder:   emb,
		})
		if err != nil {
			return nil, &mcpError{Code: -32000, Message: err.Error()}
		}
		text := formatResultsText(results, args.Topic)
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil

	case "memgraph_similar":
		var args struct {
			Text string `json:"text"`
			Top  int    `json:"top"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return nil, &mcpError{Code: -32600, Message: "invalid arguments"}
		}
		if args.Top <= 0 {
			args.Top = 5
		}
		results, err := rank.Search(db.SqlDB(), args.Text, rank.SearchOpts{
			TopN:     args.Top * 3,
			Embedder: emb,
		})
		if err != nil {
			return nil, &mcpError{Code: -32000, Message: err.Error()}
		}
		if len(results) > args.Top {
			results = results[:args.Top]
		}
		text := formatResultsText(results, args.Text)
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil

	case "memgraph_stats":
		fileCount, err := db.Stats()
		if err != nil {
			return nil, &mcpError{Code: -32000, Message: err.Error()}
		}
		dbSize, _ := db.FileSize()
		text := fmt.Sprintf("Files indexed: %d\nIndex size: %s\nIndex path: %s",
			fileCount, formatSize(dbSize), dbPath)
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil

	default:
		return nil, &mcpError{Code: -32601, Message: "unknown tool: " + p.Name}
	}
}

func formatResultsText(results []rank.Result, query string) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results for %q", query)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Results for %q (%d found):\n\n", query, len(results))
	for i, r := range results {
		tags := strings.Join(r.Tags, ", ")
		fmt.Fprintf(&sb, "%d. %s (score: %.4f)\n", i+1, r.Path, r.Score)
		if tags != "" {
			fmt.Fprintf(&sb, "   tags: %s\n", tags)
		}
		fmt.Fprintf(&sb, "   reason: %s\n\n", r.Reason)
	}
	return sb.String()
}
