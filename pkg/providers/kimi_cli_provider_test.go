package providers

import (
	"testing"
)

func TestNewKimiCliProvider(t *testing.T) {
	p := NewKimiCliProvider("/tmp/workspace")
	if p.command != "kimi" {
		t.Errorf("command = %q, want %q", p.command, "kimi")
	}
	if p.workspace != "/tmp/workspace" {
		t.Errorf("workspace = %q, want %q", p.workspace, "/tmp/workspace")
	}
}

func TestKimiCliProvider_GetDefaultModel(t *testing.T) {
	p := NewKimiCliProvider("/tmp")
	if got := p.GetDefaultModel(); got != "kimi-cli" {
		t.Errorf("GetDefaultModel() = %q, want %q", got, "kimi-cli")
	}
}

func TestKimiCliProvider_buildPrompt(t *testing.T) {
	p := NewKimiCliProvider("/tmp")

	tests := []struct {
		name     string
		messages []Message
		tools    []ToolDefinition
		want     string
	}{
		{
			name: "single user message",
			messages: []Message{
				{Role: "user", Content: "Hello!"},
			},
			want: "Hello!",
		},
		{
			name: "system and user message",
			messages: []Message{
				{Role: "system", Content: "You are helpful."},
				{Role: "user", Content: "Hello!"},
			},
			want: "## System Instructions\n\nYou are helpful.\n\n## Task\n\nHello!",
		},
		{
			name: "conversation with assistant",
			messages: []Message{
				{Role: "user", Content: "Hi"},
				{Role: "assistant", Content: "Hello!"},
				{Role: "user", Content: "How are you?"},
			},
			want: "Hi\nAssistant: Hello!\nHow are you?",
		},
		{
			name: "with tool result",
			messages: []Message{
				{Role: "user", Content: "Get weather"},
				{Role: "tool", Content: "Sunny", ToolCallID: "call_123"},
			},
			want: "Get weather\n[Tool Result for call_123]: Sunny",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.buildPrompt(tt.messages, tt.tools)
			if got != tt.want {
				t.Errorf("buildPrompt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKimiCliProvider_buildPrompt_WithTools(t *testing.T) {
	p := NewKimiCliProvider("/tmp")

	tools := []ToolDefinition{
		{
			Type: "function",
			Function: ToolFunctionDefinition{
				Name:        "get_weather",
				Description: "Get weather for a city",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"city": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
	}

	messages := []Message{
		{Role: "user", Content: "What's the weather?"},
	}

	prompt := p.buildPrompt(messages, tools)

	// Check that tools section is included
	if !contains(prompt, "## Available Tools") {
		t.Error("Expected tools section in prompt")
	}
	if !contains(prompt, "get_weather") {
		t.Error("Expected tool name in prompt")
	}
	if !contains(prompt, "Get weather for a city") {
		t.Error("Expected tool description in prompt")
	}
}

func TestKimiCliProvider_parseOutput(t *testing.T) {
	p := NewKimiCliProvider("/tmp")

	tests := []struct {
		name         string
		output       string
		wantContent  string
		wantToolCall bool
	}{
		{
			name:        "simple text response",
			output:      "Hello! How can I help you today?",
			wantContent: "Hello! How can I help you today?",
		},
		{
			name:         "response with tool call",
			output:       `{"tool_calls":[{"id":"call_123","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Beijing\"}"}}]}`,
			wantContent:  "",
			wantToolCall: true,
		},
		{
			name:        "text with tool call on separate line",
			output:      "Let me check the weather for you.\n{\"tool_calls\":[{\"id\":\"call_123\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\":\\\"Beijing\\\"}\"}}]}",
			wantContent: "Let me check the weather for you.",
			wantToolCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := p.parseOutput(tt.output)
			if err != nil {
				t.Fatalf("parseOutput() error: %v", err)
			}

			if resp.Content != tt.wantContent {
				t.Errorf("Content = %q, want %q", resp.Content, tt.wantContent)
			}

			if tt.wantToolCall && len(resp.ToolCalls) == 0 {
				t.Error("Expected tool calls but got none")
			}
			if !tt.wantToolCall && len(resp.ToolCalls) > 0 {
				t.Errorf("Expected no tool calls but got %d", len(resp.ToolCalls))
			}

			if tt.wantToolCall {
				if resp.FinishReason != "tool_calls" {
					t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "tool_calls")
				}
			} else {
				if resp.FinishReason != "stop" {
					t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
				}
			}
		})
	}
}

func TestExtractToolCallsFromText(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		wantCount    int
		wantName     string
		wantArgKey   string
		wantArgValue string
	}{
		{
			name:      "no tool calls",
			text:      "Just a simple response",
			wantCount: 0,
		},
		{
			name:         "single tool call",
			text:         `{"tool_calls":[{"id":"call_123","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Beijing\"}"}}]}`,
			wantCount:    1,
			wantName:     "get_weather",
			wantArgKey:   "city",
			wantArgValue: "Beijing",
		},
		{
			name:      "multiple tool calls",
			text:      `{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/tmp/test.txt\"}"}},{"id":"call_2","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"/tmp/out.txt\"}"}}]}`,
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolCalls := extractToolCallsFromText(tt.text)

			if len(toolCalls) != tt.wantCount {
				t.Errorf("Got %d tool calls, want %d", len(toolCalls), tt.wantCount)
			}

			if tt.wantCount > 0 && tt.wantName != "" {
				if toolCalls[0].Name != tt.wantName {
					t.Errorf("ToolCall.Name = %q, want %q", toolCalls[0].Name, tt.wantName)
				}
				if tt.wantArgKey != "" {
					if val, ok := toolCalls[0].Arguments[tt.wantArgKey]; !ok {
						t.Errorf("Missing argument key %q", tt.wantArgKey)
					} else if val != tt.wantArgValue {
						t.Errorf("Argument[%q] = %v, want %v", tt.wantArgKey, val, tt.wantArgValue)
					}
				}
			}
		})
	}
}

func TestStripToolCallsFromText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "no tool calls",
			text: "Just a simple response",
			want: "Just a simple response",
		},
		{
			name: "only tool call",
			text: `{"tool_calls":[{"id":"call_123","type":"function","function":{"name":"get_weather","arguments":"{}"}}]}`,
			want: "",
		},
		{
			name: "text with tool call",
			text: "Let me check.\n{\"tool_calls\":[{\"id\":\"call_123\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{}\"}}]}",
			want: "Let me check.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripToolCallsFromText(tt.text)
			if got != tt.want {
				t.Errorf("stripToolCallsFromText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKimiCliProvider_Chat_ContextCancellation(t *testing.T) {
	// This test would require mocking the exec.Command, which is complex
	// For now, we just verify the provider structure is correct
	p := NewKimiCliProvider("/tmp")
	if p == nil {
		t.Fatal("Provider should not be nil")
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	if start+len(substr) > len(s) {
		return false
	}
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
