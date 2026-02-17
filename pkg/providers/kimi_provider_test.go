package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewKimiProvider(t *testing.T) {
	// Test with default API base
	p := NewKimiProvider("test-key", "", "")
	if p.apiKey != "test-key" {
		t.Errorf("apiKey = %q, want %q", p.apiKey, "test-key")
	}
	if p.apiBase != kimiDefaultAPIBase {
		t.Errorf("apiBase = %q, want %q", p.apiBase, kimiDefaultAPIBase)
	}

	// Test with custom API base
	p2 := NewKimiProvider("test-key", "https://custom.api.com/v1", "")
	if p2.apiBase != "https://custom.api.com/v1" {
		t.Errorf("apiBase = %q, want %q", p2.apiBase, "https://custom.api.com/v1")
	}

	// Test trimming trailing slash
	p3 := NewKimiProvider("test-key", "https://custom.api.com/v1/", "")
	if p3.apiBase != "https://custom.api.com/v1" {
		t.Errorf("apiBase = %q, want %q", p3.apiBase, "https://custom.api.com/v1")
	}
}

func TestKimiProvider_GetDefaultModel(t *testing.T) {
	p := NewKimiProvider("test-key", "", "")
	if got := p.GetDefaultModel(); got != kimiDefaultModel {
		t.Errorf("GetDefaultModel() = %q, want %q", got, kimiDefaultModel)
	}
}

func TestResolveKimiModel(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"kimi prefix", "kimi/kimi-k2.5", "kimi-k2.5"},
		{"moonshot prefix", "moonshot/kimi-k2.5", "kimi-k2.5"},
		{"no prefix", "kimi-k2.5", "kimi-k2.5"},
		{"with version", "kimi-k2.5-202501", "kimi-k2.5-202501"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveKimiModel(tt.input)
			if got != tt.expected {
				t.Errorf("resolveKimiModel(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestKimiProvider_Chat_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("Expected path /chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("Expected Authorization header 'Bearer test-api-key', got %s", r.Header.Get("Authorization"))
		}

		// Verify request body
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("Failed to decode request body: %v", err)
		}

		response := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "kimi-k2.5",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "Hello! I'm Kimi, how can I help you today?",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 15,
				"total_tokens":      25,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	provider := NewKimiProvider("test-api-key", server.URL, "")
	
	messages := []Message{
		{Role: "user", Content: "Hello!"},
	}
	
	resp, err := provider.Chat(context.Background(), messages, nil, "kimi-k2.5", map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.7,
	})
	
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	
	if resp.Content != "Hello! I'm Kimi, how can I help you today?" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello! I'm Kimi, how can I help you today?")
	}
	
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	
	if resp.Usage == nil {
		t.Fatal("Usage should not be nil")
	}
	
	if resp.Usage.TotalTokens != 25 {
		t.Errorf("TotalTokens = %d, want 25", resp.Usage.TotalTokens)
	}
}

func TestKimiProvider_Chat_WithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "kimi-k2.5",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]interface{}{
							{
								"id":   "call_abc123",
								"type": "function",
								"function": map[string]interface{}{
									"name":      "get_weather",
									"arguments": `{"city": "Beijing"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     20,
				"completion_tokens": 30,
				"total_tokens":      50,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	provider := NewKimiProvider("test-api-key", server.URL, "")
	
	messages := []Message{
		{Role: "user", Content: "What's the weather in Beijing?"},
	}
	
	tools := []ToolDefinition{
		{
			Type: "function",
			Function: ToolFunctionDefinition{
				Name:        "get_weather",
				Description: "Get weather information for a city",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"city": map[string]interface{}{
							"type": "string",
						},
					},
					"required": []string{"city"},
				},
			},
		},
	}
	
	resp, err := provider.Chat(context.Background(), messages, tools, "kimi-k2.5", nil)
	
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	
	tc := resp.ToolCalls[0]
	if tc.Name != "get_weather" {
		t.Errorf("ToolCall.Name = %q, want %q", tc.Name, "get_weather")
	}
	
	if tc.ID != "call_abc123" {
		t.Errorf("ToolCall.ID = %q, want %q", tc.ID, "call_abc123")
	}
	
	if resp.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "tool_calls")
	}
}

func TestKimiProvider_Chat_NoAPIKey(t *testing.T) {
	provider := NewKimiProvider("", "", "")
	
	messages := []Message{
		{Role: "user", Content: "Hello!"},
	}
	
	_, err := provider.Chat(context.Background(), messages, nil, "kimi-k2.5", nil)
	
	if err == nil {
		t.Fatal("Expected error when API key is not configured")
	}
	
	if err.Error() != "Kimi API key not configured" {
		t.Errorf("Error message = %q, want %q", err.Error(), "Kimi API key not configured")
	}
}

func TestKimiProvider_Chat_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"message": "Invalid API key"}}`))
	}))
	defer server.Close()

	provider := NewKimiProvider("invalid-key", server.URL, "")
	
	messages := []Message{
		{Role: "user", Content: "Hello!"},
	}
	
	_, err := provider.Chat(context.Background(), messages, nil, "kimi-k2.5", nil)
	
	if err == nil {
		t.Fatal("Expected error when API returns error")
	}
}

func TestKimiProvider_Chat_K2ModelTemperature(t *testing.T) {
	var capturedBody map[string]interface{}
	
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		
		response := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "Response",
					},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	provider := NewKimiProvider("test-key", server.URL, "")
	
	messages := []Message{{Role: "user", Content: "Hello"}}
	
	// Test with k2 model - temperature should be forced to 1.0
	provider.Chat(context.Background(), messages, nil, "kimi-k2.5", map[string]interface{}{
		"temperature": 0.5,
	})
	
	if capturedBody["temperature"] != 1.0 {
		t.Errorf("Temperature for k2 model = %v, want 1.0", capturedBody["temperature"])
	}
	
	// Test with non-k2 model - temperature should be preserved
	provider.Chat(context.Background(), messages, nil, "kimi-k1.5", map[string]interface{}{
		"temperature": 0.5,
	})
	
	if capturedBody["temperature"] != 0.5 {
		t.Errorf("Temperature for non-k2 model = %v, want 0.5", capturedBody["temperature"])
	}
}

func TestKimiProvider_TranslateTools(t *testing.T) {
	provider := NewKimiProvider("test-key", "", "")
	
	tools := []ToolDefinition{
		{
			Type: "function",
			Function: ToolFunctionDefinition{
				Name:        "read_file",
				Description: "Read a file",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type": "string",
						},
					},
				},
			},
		},
	}
	
	translated := provider.translateTools(tools)
	
	if len(translated) != 1 {
		t.Fatalf("Expected 1 translated tool, got %d", len(translated))
	}
	
	if translated[0]["type"] != "function" {
		t.Errorf("Tool type = %v, want 'function'", translated[0]["type"])
	}
	
	fn, ok := translated[0]["function"].(map[string]interface{})
	if !ok {
		t.Fatal("Function should be a map")
	}
	
	if fn["name"] != "read_file" {
		t.Errorf("Function name = %v, want 'read_file'", fn["name"])
	}
}
