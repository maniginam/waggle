package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/maniginam/waggle/internal/bridge"
)

// Client implements the Bridge interface for OpenAI-compatible APIs.
// Works with OpenAI, Grok (api.x.ai), and Ollama (localhost:11434).
type Client struct {
	baseURL      string
	apiKey       string
	defaultModel string
	caps         []bridge.Capability
	http         *http.Client
}

func New(baseURL, apiKey, defaultModel string, caps []bridge.Capability) *Client {
	if caps == nil {
		caps = []bridge.Capability{bridge.CapChat}
	}
	return &Client{
		baseURL:      baseURL,
		apiKey:       apiKey,
		defaultModel: defaultModel,
		caps:         caps,
		http:         &http.Client{},
	}
}

func (c *Client) Chat(ctx context.Context, messages []bridge.Message, opts bridge.ChatOpts) (string, error) {
	model := c.defaultModel
	if opts.Model != "" {
		model = opts.Model
	}

	var apiMsgs []map[string]string
	if opts.SystemPrompt != "" {
		apiMsgs = append(apiMsgs, map[string]string{"role": "system", "content": opts.SystemPrompt})
	}
	for _, m := range messages {
		apiMsgs = append(apiMsgs, map[string]string{"role": m.Role, "content": m.Content})
	}

	reqBody := map[string]any{
		"model":    model,
		"messages": apiMsgs,
	}
	if opts.MaxTokens > 0 {
		reqBody["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature > 0 {
		reqBody["temperature"] = opts.Temperature
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

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
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}

func (c *Client) Capabilities() []bridge.Capability { return c.caps }
func (c *Client) Provider() string                  { return "openai" }
