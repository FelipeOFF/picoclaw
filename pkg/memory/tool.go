package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sipeed/picoclaw/pkg/tools"
)

// MemoryTool provides memory recall and storage capabilities
type MemoryTool struct {
	store *MemoryStore
}

// NewMemoryTool creates a new memory tool
func NewMemoryTool(store *MemoryStore) *MemoryTool {
	return &MemoryTool{store: store}
}

// Name returns the tool name
func (t *MemoryTool) Name() string {
	return "memory_recall"
}

// Description returns the tool description
func (t *MemoryTool) Description() string {
	return "Search through long-term memories. Use when you need context about user preferences, past decisions, or previously discussed topics."
}

// Schema returns the JSON schema for parameters
func (t *MemoryTool) Schema() string {
	return `{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Search query to find relevant memories"
			},
			"limit": {
				"type": "number",
				"description": "Maximum number of results (default: 5)"
			},
			"category": {
				"type": "string",
				"enum": ["preference", "decision", "entity", "fact", "other"],
				"description": "Filter by memory category (optional)"
			}
		},
		"required": ["query"]
	}`
}

// Execute runs the memory recall
func (t *MemoryTool) Execute(ctx context.Context, params map[string]interface{}) *tools.ToolResult {
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return &tools.ToolResult{
			Err: fmt.Errorf("query parameter is required"),
		}
	}

	limit := 5
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	}

	results, err := t.store.Search(query, limit, 0.5)
	if err != nil {
		return &tools.ToolResult{
			Err: fmt.Errorf("memory search failed: %w", err),
		}
	}

	if len(results) == 0 {
		return &tools.ToolResult{
			ForLLM:  "No relevant memories found.",
			ForUser: "",
		}
	}

	// Format results
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant memories:\n\n", len(results)))
	
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. [%s] (score: %.2f) %s\n", 
			i+1, r.Entry.Category, r.Score, r.Entry.Text))
	}

	return &tools.ToolResult{
		ForLLM:  sb.String(),
		ForUser: "",
	}
}

// MemoryCaptureTool provides automatic memory capture
type MemoryCaptureTool struct {
	store *MemoryStore
}

// NewMemoryCaptureTool creates a new memory capture tool
func NewMemoryCaptureTool(store *MemoryStore) *MemoryCaptureTool {
	return &MemoryCaptureTool{store: store}
}

// Name returns the tool name
func (t *MemoryCaptureTool) Name() string {
	return "memory_capture"
}

// Description returns the tool description
func (t *MemoryCaptureTool) Description() string {
	return "Store important information in long-term memory. Use for user preferences, decisions, or facts that should be remembered."
}

// Schema returns the JSON schema for parameters
func (t *MemoryCaptureTool) Schema() string {
	return `{
		"type": "object",
		"properties": {
			"text": {
				"type": "string",
				"description": "The information to remember"
			},
			"category": {
				"type": "string",
				"enum": ["preference", "decision", "entity", "fact", "other"],
				"description": "Category of the memory"
			},
			"importance": {
				"type": "number",
				"description": "Importance level 0-1 (default: 0.5)"
			}
		},
		"required": ["text"]
	}`
}

// Execute runs the memory capture
func (t *MemoryCaptureTool) Execute(ctx context.Context, params map[string]interface{}) *tools.ToolResult {
	text, ok := params["text"].(string)
	if !ok || text == "" {
		return &tools.ToolResult{
			Err: fmt.Errorf("text parameter is required"),
		}
	}

	category := "other"
	if c, ok := params["category"].(string); ok {
		category = c
	}

	importance := float32(0.5)
	if i, ok := params["importance"].(float64); ok {
		importance = float32(i)
	}

	entry, err := t.store.Store(text, importance, category, "")
	if err != nil {
		return &tools.ToolResult{
			Err: fmt.Errorf("failed to store memory: %w", err),
		}
	}

	return &tools.ToolResult{
		ForLLM:  fmt.Sprintf("Memory stored successfully (ID: %s)", entry.ID),
		ForUser: "",
	}
}

// AutoCapture handles automatic memory capture from conversation
// Returns true if a memory was captured
type AutoCapture struct {
	store *MemoryStore
}

// NewAutoCapture creates a new auto-capture handler
func NewAutoCapture(store *MemoryStore) *AutoCapture {
	return &AutoCapture{store: store}
}

// ShouldCapture determines if text should be captured as memory
func (ac *AutoCapture) ShouldCapture(text string) bool {
	// Length check
	if len(text) < 10 || len(text) > 500 {
		return false
	}

	// Skip if contains memory recall markers
	if strings.Contains(text, "<relevant-memories>") {
		return false
	}

	// Skip system-generated content
	if strings.HasPrefix(text, "<") && strings.Contains(text, "</") {
		return false
	}

	// Skip emoji-heavy responses
	emojiCount := 0
	for _, r := range text {
		if r >= 0x1F300 && r <= 0x1F9FF {
			emojiCount++
		}
	}
	if emojiCount > 3 {
		return false
	}

	// Check memory triggers
	triggers := []*regexp.Regexp{
		regexp.MustCompile(`(?i)remember|zapamatuj|pamatuj`),
		regexp.MustCompile(`(?i)prefer|radši|like|love|hate|want|need`),
		regexp.MustCompile(`(?i)decided|rozhodli|will use|budeme`),
		regexp.MustCompile(`\+\d{10,}`),                           // Phone numbers
		regexp.MustCompile(`[\w.-]+@[\w.-]+\.\w+`),                // Emails
		regexp.MustCompile(`(?i)můj\s+\w+\s+je|je\s+můj`),         // "my X is"
		regexp.MustCompile(`(?i)my\s+\w+\s+is|is\s+my`),           // "my X is"
		regexp.MustCompile(`(?i)always|never|important`),
	}

	for _, trigger := range triggers {
		if trigger.MatchString(text) {
			return true
		}
	}

	return false
}

// DetectCategory determines the category of a memory
func (ac *AutoCapture) DetectCategory(text string) string {
	lower := strings.ToLower(text)

	if regexp.MustCompile(`(?i)prefer|radši|like|love|hate|want`).MatchString(lower) {
		return "preference"
	}
	if regexp.MustCompile(`(?i)decided|rozhodli|will use|budeme`).MatchString(lower) {
		return "decision"
	}
	if regexp.MustCompile(`(?i)\+\d{10,}|@[\w.-]+\.\w+|is called|jmenuje se`).MatchString(lower) {
		return "entity"
	}
	if regexp.MustCompile(`(?i)is|are|has|have|je|má|jsou`).MatchString(lower) {
		return "fact"
	}

	return "other"
}

// Capture captures a memory from text
func (ac *AutoCapture) Capture(text, sessionKey string) (*MemoryEntry, error) {
	if !ac.ShouldCapture(text) {
		return nil, nil
	}

	category := ac.DetectCategory(text)
	
	// Calculate importance based on triggers
	importance := float32(0.5)
	if strings.Contains(strings.ToLower(text), "important") {
		importance = 0.8
	}
	if regexp.MustCompile(`(?i)always|never`).MatchString(text) {
		importance = 0.7
	}

	return ac.store.Store(text, importance, category, sessionKey)
}

// ToTool converts to tools.Tool interface
func (t *MemoryTool) ToTool() tools.Tool {
	return &memoryToolAdapter{tool: t}
}

// ToTool converts to tools.Tool interface
func (t *MemoryCaptureTool) ToTool() tools.Tool {
	return &memoryToolAdapter{tool: t}
}

// ToolAdapter adapts memory tools to the tools.Tool interface
type memoryToolAdapter struct {
	tool interface {
		Name() string
		Description() string
		Schema() string
		Execute(ctx context.Context, params map[string]interface{}) *tools.ToolResult
	}
}

func (a *memoryToolAdapter) Name() string {
	return a.tool.Name()
}

func (a *memoryToolAdapter) Description() string {
	return a.tool.Description()
}

func (a *memoryToolAdapter) Schema() string {
	return a.tool.Schema()
}

func (a *memoryToolAdapter) Parameters() map[string]interface{} {
	// Parse JSON schema into map
	var params map[string]interface{}
	json.Unmarshal([]byte(a.tool.Schema()), &params)
	return params
}

func (a *memoryToolAdapter) Execute(ctx context.Context, params map[string]interface{}) *tools.ToolResult {
	return a.tool.Execute(ctx, params)
}

func (a *memoryToolAdapter) SetContext(channel, chatID string) {}

// RegisterMemoryTools registers memory tools with the registry
func RegisterMemoryTools(registry *tools.ToolRegistry, store *MemoryStore) {
	if store == nil {
		return
	}

	recallTool := NewMemoryTool(store)
	captureTool := NewMemoryCaptureTool(store)

	registry.Register(recallTool.ToTool())
	registry.Register(captureTool.ToTool())
}
