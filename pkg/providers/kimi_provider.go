// PicoClaw - Ultra-lightweight personal AI agent
// Kimi Provider - Moonshot AI (Kimi) LLM Provider
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

const kimiDefaultModel = "kimi-k2.5"
const kimiDefaultAPIBase = "https://api.moonshot.cn/v1"

type KimiProvider struct {
	apiKey     string
	apiBase    string
	proxy      string
	httpClient *http.Client
}

func NewKimiProvider(apiKey, apiBase, proxy string) *KimiProvider {
	if apiBase == "" {
		apiBase = kimiDefaultAPIBase
	}

	client := &http.Client{
		Timeout: 120 * time.Second,
	}

	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err == nil {
			client.Transport = &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			}
		}
	}

	return &KimiProvider{
		apiKey:     apiKey,
		apiBase:    strings.TrimRight(apiBase, "/"),
		proxy:      proxy,
		httpClient: client,
	}
}

func (p *KimiProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Kimi API key not configured")
	}

	// Resolve model name - strip provider prefix if present
	resolvedModel := resolveKimiModel(model)
	if resolvedModel != model {
		logger.DebugCF("provider.kimi", "Model resolved", map[string]interface{}{
			"requested_model": model,
			"resolved_model":  resolvedModel,
		})
	}

	requestBody := map[string]interface{}{
		"model":    resolvedModel,
		"messages": messages,
	}

	if len(tools) > 0 {
		requestBody["tools"] = p.translateTools(tools)
		requestBody["tool_choice"] = "auto"
	}

	// Handle max_tokens - Kimi uses max_completion_tokens
	if maxTokens, ok := options["max_tokens"].(int); ok && maxTokens > 0 {
		requestBody["max_completion_tokens"] = maxTokens
	}

	// Handle temperature - Kimi k2 models only support temperature=1
	if temperature, ok := options["temperature"].(float64); ok {
		lowerModel := strings.ToLower(resolvedModel)
		if strings.Contains(lowerModel, "k2") {
			requestBody["temperature"] = 1.0
			logger.DebugCF("provider.kimi", "Kimi k2 model detected, using temperature=1", map[string]interface{}{
				"model":       resolvedModel,
				"requested":   temperature,
				"actual":      1.0,
			})
		} else {
			requestBody["temperature"] = temperature
		}
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiBase+"/chat/completions", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	logger.DebugCF("provider.kimi", "Sending request", map[string]interface{}{
		"model":          resolvedModel,
		"messages_count": len(messages),
		"tools_count":    len(tools),
	})

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.ErrorCF("provider.kimi", "API request failed", map[string]interface{}{
			"status_code": resp.StatusCode,
			"body":        string(body),
		})
		return nil, fmt.Errorf("Kimi API request failed (status %d): %s", resp.StatusCode, string(body))
	}

	return p.parseResponse(body)
}

func (p *KimiProvider) parseResponse(body []byte) (*LLMResponse, error) {
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function *struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *UsageInfo `json:"usage"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return &LLMResponse{
			Content:      "",
			FinishReason: "stop",
		}, nil
	}

	choice := apiResponse.Choices[0]

	toolCalls := make([]ToolCall, 0, len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		arguments := make(map[string]interface{})
		name := ""

		// Handle function call format
		if tc.Type == "function" && tc.Function != nil {
			name = tc.Function.Name
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &arguments); err != nil {
					arguments["raw"] = tc.Function.Arguments
				}
			}
		}

		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      name,
			Arguments: arguments,
		})
	}

	return &LLMResponse{
		Content:      choice.Message.Content,
		ToolCalls:    toolCalls,
		FinishReason: choice.FinishReason,
		Usage:        apiResponse.Usage,
	}, nil
}

func (p *KimiProvider) translateTools(tools []ToolDefinition) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		tool := map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  t.Function.Parameters,
			},
		}
		result = append(result, tool)
	}
	return result
}

func (p *KimiProvider) GetDefaultModel() string {
	return kimiDefaultModel
}

// resolveKimiModel resolves model name, stripping provider prefix if present
func resolveKimiModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	
	// Strip provider prefix
	if strings.HasPrefix(m, "kimi/") {
		return strings.TrimPrefix(model, "kimi/")
	}
	if strings.HasPrefix(m, "moonshot/") {
		return strings.TrimPrefix(model, "moonshot/")
	}
	
	return model
}
