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

	// Note: Kimi CLI has its own tool system, we don't pass tools through the prompt
	// as it uses a different format. The CLI will use its built-in tools.
	prompt := p.buildPrompt(messages, nil)

	args := []string{
		"--quiet", // Alias for --print --output-format text --final-message-only
		"--yolo",
	}

	if model != "" && model != "kimi-cli" {
		args = append(args, "--model", model)
	}

	if p.workspace != "" {
		args = append(args, "--work-dir", p.workspace)
	}

	// Pass prompt via stdin to avoid "argument list too long" error
	cmd := exec.CommandContext(ctx, p.command, args...)
	cmd.Stdin = bytes.NewReader([]byte(prompt))

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
// For Telegram/chat use, we only send the LAST user message to avoid
// the CLI echoing back the entire conversation history.
func (p *KimiCliProvider) buildPrompt(messages []Message, tools []ToolDefinition) string {
	// Find the last user message - this is what we want to respond to
	var lastUserMessage string
	var systemPrompt string
	
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "user" && lastUserMessage == "" {
			lastUserMessage = msg.Content
		}
		if msg.Role == "system" && systemPrompt == "" {
			systemPrompt = msg.Content
		}
	}

	// If no user message found, return empty
	if lastUserMessage == "" {
		return ""
	}

	var sb strings.Builder

	// Add condensed system prompt (just the essential parts)
	if systemPrompt != "" {
		// Extract just the first paragraph or key instructions
		lines := strings.Split(systemPrompt, "\n")
		var essentialLines []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			// Skip markdown headers and empty lines
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
				continue
			}
			essentialLines = append(essentialLines, line)
			if len(essentialLines) >= 3 {
				break
			}
		}
		if len(essentialLines) > 0 {
			sb.WriteString(strings.Join(essentialLines, ". "))
			sb.WriteString("\n\n")
		}
	}

	if len(tools) > 0 {
		sb.WriteString(p.buildToolsPrompt(tools))
		sb.WriteString("\n\n")
	}

	// Just the user message - no conversation history
	sb.WriteString(lastUserMessage)
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

// Max response length to prevent excessive output
const maxKimiResponseLength = 10000 // 10K characters max

// parseOutput processes the output from kimi --quiet.
// With --quiet flag, the output should be clean text (final assistant message only).
func (p *KimiCliProvider) parseOutput(output string) (*LLMResponse, error) {
	content := strings.TrimSpace(output)
	
	// With --quiet, output should be clean, but let's handle edge cases
	// Remove any remaining debug prefixes or suffixes
	content = cleanKimiOutput(content)
	
	// Truncate if response is excessively long
	if len(content) > maxKimiResponseLength {
		content = content[:maxKimiResponseLength] + 
			"\n\n[Response truncated due to excessive length. Please be more specific in your request.]"
	}
	
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

// cleanKimiOutput removes any debug artifacts that might still appear
func cleanKimiOutput(content string) string {
	// Remove common debug prefixes
	lines := strings.Split(content, "\n")
	var result []string
	
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Skip debug lines
		if strings.HasPrefix(trimmed, "TurnBegin(") ||
			strings.HasPrefix(trimmed, "StepBegin(") ||
			strings.HasPrefix(trimmed, "StatusUpdate(") ||
			strings.HasPrefix(trimmed, "TurnEnd(") ||
			strings.HasPrefix(trimmed, "userinput=") ||
			strings.HasPrefix(trimmed, "type='text'") ||
			strings.HasPrefix(trimmed, "contextusage=") ||
			strings.HasPrefix(trimmed, "tokenusage=") ||
			strings.HasPrefix(trimmed, "message_id=") ||
			trimmed == ")" ||
			trimmed == "(" {
			continue
		}
		
		// Skip lines that look like debug output
		if strings.Contains(trimmed, "TextPart(") && strings.Contains(trimmed, "type='text'") {
			// Extract just the text content if present
			if idx := strings.Index(trimmed, "text='"); idx != -1 {
				start := idx + 6
				end := strings.LastIndex(trimmed[start:], "'")
				if end != -1 {
					text := trimmed[start : start+end]
					text = strings.ReplaceAll(text, "\\n", "\n")
					result = append(result, text)
					continue
				}
			}
			continue
		}
		
		result = append(result, line)
	}
	
	return strings.Join(result, "\n")
}

