package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/maniginam/waggle/internal/bridge"
)

const defaultBaseURL = "https://api.anthropic.com"

type Client struct {
	baseURL      string
	apiKey       string
	defaultModel string
	http         *http.Client
}

func New(apiKey, defaultModel string) *Client {
	return &Client{
		baseURL:      defaultBaseURL,
		apiKey:       apiKey,
		defaultModel: defaultModel,
		http:         &http.Client{},
	}
}

func (c *Client) Chat(ctx context.Context, messages []bridge.Message, opts bridge.ChatOpts) (string, error) {
	model := c.defaultModel
	if opts.Model != "" {
		model = opts.Model
	}

	var apiMsgs []map[string]string
	for _, m := range messages {
		if m.Role == "system" {
			continue // handled via top-level system field
		}
		apiMsgs = append(apiMsgs, map[string]string{"role": m.Role, "content": m.Content})
	}

	reqBody := map[string]any{
		"model":      model,
		"messages":   apiMsgs,
		"max_tokens": 4096,
	}
	if opts.MaxTokens > 0 {
		reqBody["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}
	if opts.SystemPrompt != "" {
		reqBody["system"] = opts.SystemPrompt
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("no content in response")
	}

	// Concatenate all text blocks
	var text string
	for _, block := range result.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text, nil
}

func (c *Client) Capabilities() []bridge.Capability {
	return []bridge.Capability{bridge.CapChat, bridge.CapCode}
}

func (c *Client) Provider() string { return "claude-api" }
