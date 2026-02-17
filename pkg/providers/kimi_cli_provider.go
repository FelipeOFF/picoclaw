package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// KimiCliProvider implements LLMProvider by wrapping the kimi CLI as a subprocess.
type KimiCliProvider struct {
	command   string
	workspace string
}

// NewKimiCliProvider creates a new Kimi CLI provider.
func NewKimiCliProvider(workspace string) *KimiCliProvider {
	return &KimiCliProvider{
		command:   "kimi",
		workspace: workspace,
	}
}

// Chat implements LLMProvider.Chat by executing the kimi CLI in non-interactive mode.
func (p *KimiCliProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	if p.command == "" {
		return nil, fmt.Errorf("kimi command not configured")
	}

	prompt := p.buildPrompt(messages, tools)

	args := []string{
		"--print",
		"--yolo",
	}

	if model != "" && model != "kimi-cli" {
		args = append(args, "--model", model)
	}

	if p.workspace != "" {
		args = append(args, "--work-dir", p.workspace)
	}

	args = append(args, "--prompt", prompt)

	cmd := exec.CommandContext(ctx, p.command, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Parse output even if exit code is non-zero,
	// because kimi may write diagnostic info to stderr but still produce valid output.
	if stdoutStr := stdout.String(); stdoutStr != "" {
		resp, parseErr := p.parseOutput(stdoutStr)
		if parseErr == nil && resp != nil && (resp.Content != "" || len(resp.ToolCalls) > 0) {
			return resp, nil
		}
	}

	if err != nil {
		if ctx.Err() == context.Canceled {
			return nil, ctx.Err()
		}
		if stderrStr := stderr.String(); stderrStr != "" {
			return nil, fmt.Errorf("kimi cli error: %s", stderrStr)
		}
		return nil, fmt.Errorf("kimi cli error: %w", err)
	}

	return p.parseOutput(stdout.String())
}

// GetDefaultModel returns the default model identifier.
func (p *KimiCliProvider) GetDefaultModel() string {
	return "kimi-cli"
}

// buildPrompt converts messages to a prompt string for the Kimi CLI.
// System messages are prepended as instructions.
func (p *KimiCliProvider) buildPrompt(messages []Message, tools []ToolDefinition) string {
	var systemParts []string
	var conversationParts []string

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			systemParts = append(systemParts, msg.Content)
		case "user":
			conversationParts = append(conversationParts, msg.Content)
		case "assistant":
			conversationParts = append(conversationParts, "Assistant: "+msg.Content)
		case "tool":
			conversationParts = append(conversationParts,
				fmt.Sprintf("[Tool Result for %s]: %s", msg.ToolCallID, msg.Content))
		}
	}

	var sb strings.Builder

	if len(systemParts) > 0 {
		sb.WriteString("## System Instructions\n\n")
		sb.WriteString(strings.Join(systemParts, "\n\n"))
		sb.WriteString("\n\n## Task\n\n")
	}

	if len(tools) > 0 {
		sb.WriteString(p.buildToolsPrompt(tools))
		sb.WriteString("\n\n")
	}

	// Simplify single user message (no prefix)
	if len(conversationParts) == 1 && len(systemParts) == 0 && len(tools) == 0 {
		return conversationParts[0]
	}

	sb.WriteString(strings.Join(conversationParts, "\n"))
	return sb.String()
}

// buildToolsPrompt creates a tool definitions section for the prompt.
func (p *KimiCliProvider) buildToolsPrompt(tools []ToolDefinition) string {
	var sb strings.Builder

	sb.WriteString("## Available Tools\n\n")
	sb.WriteString("When you need to use a tool, respond with ONLY a JSON object:\n\n")
	sb.WriteString("```json\n")
	sb.WriteString(`{"tool_calls":[{"id":"call_xxx","type":"function","function":{"name":"tool_name","arguments":"{...}"}}]}`)
	sb.WriteString("\n```\n\n")
	sb.WriteString("CRITICAL: The 'arguments' field MUST be a JSON-encoded STRING.\n\n")
	sb.WriteString("### Tool Definitions:\n\n")

	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		sb.WriteString(fmt.Sprintf("#### %s\n", tool.Function.Name))
		if tool.Function.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", tool.Function.Description))
		}
		if len(tool.Function.Parameters) > 0 {
			paramsJSON, _ := json.Marshal(tool.Function.Parameters)
			sb.WriteString(fmt.Sprintf("Parameters:\n```json\n%s\n```\n", string(paramsJSON)))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// parseOutput processes the output from kimi --print.
func (p *KimiCliProvider) parseOutput(output string) (*LLMResponse, error) {
	// The kimi CLI in print mode outputs the response directly
	// We need to extract tool calls from the text (similar to ClaudeCliProvider)
	
	content := strings.TrimSpace(output)
	
	// Extract tool calls from response text
	toolCalls := extractToolCallsFromText(content)
	
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		content = stripToolCallsFromText(content)
	}

	return &LLMResponse{
		Content:      strings.TrimSpace(content),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage:        nil, // Kimi CLI doesn't provide usage info in print mode
	}, nil
}


