// Integration example for memory system
// This shows how to integrate memory into the agent loop

package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// MemoryEnhancedAgent wraps an agent with memory capabilities
type MemoryEnhancedAgent struct {
	inner      *agent.AgentLoop
	memory     *MemoryStore
	autoCapture *AutoCapture
}

// NewMemoryEnhancedAgent creates an agent with memory support
func NewMemoryEnhancedAgent(baseAgent *agent.AgentLoop, memoryStore *MemoryStore) *MemoryEnhancedAgent {
	return &MemoryEnhancedAgent{
		inner:       baseAgent,
		memory:      memoryStore,
		autoCapture: NewAutoCapture(memoryStore),
	}
}

// ProcessMessage processes a message with memory enhancement
func (a *MemoryEnhancedAgent) ProcessMessage(ctx context.Context, sessionKey, channel, chatID, content string) (string, error) {
	// 1. Search for relevant memories
	memories, err := a.memory.Search(content, 3, 0.6)
	if err != nil {
		logger.WarnCF("memory", "Failed to search memories", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// 2. Enhance prompt with memories
	enhancedContent := a.enhanceWithMemories(content, memories)

	// 3. Process with inner agent
	response, err := a.inner.ProcessDirectWithChannel(ctx, enhancedContent, sessionKey, channel, chatID)
	if err != nil {
		return "", err
	}

	// 4. Auto-capture important information from user message
	if a.autoCapture.ShouldCapture(content) {
		_, err := a.autoCapture.Capture(content, sessionKey)
		if err != nil {
			logger.WarnCF("memory", "Failed to auto-capture memory", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	// 5. Auto-capture from response if important
	if a.autoCapture.ShouldCapture(response) {
		_, err := a.autoCapture.Capture(response, sessionKey)
		if err != nil {
			logger.WarnCF("memory", "Failed to auto-capture response", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	return response, nil
}

// enhanceWithMemories adds relevant memories to the prompt
func (a *MemoryEnhancedAgent) enhanceWithMemories(content string, memories []MemorySearchResult) string {
	if len(memories) == 0 {
		return content
	}

	var sb strings.Builder
	sb.WriteString("<relevant-memories>\n")
	sb.WriteString("The following information from previous conversations may be relevant:\n\n")

	for i, m := range memories {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, m.Entry.Category, m.Entry.Text))
	}

	sb.WriteString("</relevant-memories>\n\n")
	sb.WriteString(content)

	return sb.String()
}

// SetupMemoryIntegration configures memory for an agent
func SetupMemoryIntegration(workspace string, apiKey string) (*MemoryStore, error) {
	config := DefaultConfig(workspace)
	
	// Use OpenAI if API key provided, otherwise use simple embedder
	if apiKey != "" {
		config.EmbeddingProvider = "openai"
		config.OpenAIAPIKey = apiKey
		config.EmbeddingModel = "text-embedding-3-small"
	} else {
		// Fallback to simple embedder (no external dependencies)
		logger.InfoC("memory", "Using simple embedder (no API key provided)")
	}

	store, err := NewMemoryStore(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory store: %w", err)
	}

	return store, nil
}

// MemoryContextEnhancer enhances message context with memories
type MemoryContextEnhancer struct {
	store *MemoryStore
}

// NewMemoryContextEnhancer creates a new enhancer
func NewMemoryContextEnhancer(store *MemoryStore) *MemoryContextEnhancer {
	return &MemoryContextEnhancer{store: store}
}

// Enhance adds memory context to a message
func (e *MemoryContextEnhancer) Enhance(ctx context.Context, content string) string {
	memories, err := e.store.Search(content, 3, 0.6)
	if err != nil || len(memories) == 0 {
		return content
	}

	var sb strings.Builder
	sb.WriteString("## Relevant Context from Memory\n\n")
	for _, m := range memories {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", m.Entry.Category, m.Entry.Text))
	}
	sb.WriteString("\n---\n\n")
	sb.WriteString(content)

	return sb.String()
}

// RegisterWithToolRegistry registers memory tools
func RegisterWithToolRegistry(registry *tools.ToolRegistry, store *MemoryStore) {
	if store == nil {
		return
	}

	RegisterMemoryTools(registry, store)
	logger.InfoC("memory", "Memory tools registered")
}
