package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/maniginam/waggle/internal/bridge"
)

// modelMap maps friendly names to Bedrock model IDs.
var modelMap = map[string]string{
	"claude-sonnet-4-6": "anthropic.claude-sonnet-4-6-v1",
	"claude-opus-4-6":   "anthropic.claude-opus-4-6-v1",
	"claude-haiku-4-5":  "anthropic.claude-haiku-4-5-v1",
}

// Client implements bridge.Bridge for AWS Bedrock.
type Client struct {
	region       string
	accessKey    string
	secretKey    string
	defaultModel string
	endpointURL  string // override for testing
	http         *http.Client
}

// New creates a new Bedrock client.
func New(region, accessKey, secretKey, defaultModel string) *Client {
	return &Client{
		region:       region,
		accessKey:    accessKey,
		secretKey:    secretKey,
		defaultModel: defaultModel,
		http:         &http.Client{},
	}
}

// Chat sends messages to the Bedrock API and returns the response text.
func (c *Client) Chat(ctx context.Context, messages []bridge.Message, opts bridge.ChatOpts) (string, error) {
	model := c.defaultModel
	if opts.Model != "" {
		model = opts.Model
	}

	// Map friendly name to Bedrock model ID
	if bedrockID, ok := modelMap[model]; ok {
		model = bedrockID
	}

	var apiMsgs []map[string]string
	for _, m := range messages {
		if m.Role == "system" {
			continue
		}
		apiMsgs = append(apiMsgs, map[string]string{"role": m.Role, "content": m.Content})
	}

	reqBody := map[string]any{
		"anthropic_version": "bedrock-2023-10-16",
		"messages":          apiMsgs,
		"max_tokens":        4096,
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

	endpoint := c.endpointURL
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", c.region)
	}
	url := fmt.Sprintf("%s/model/%s/invoke", endpoint, model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	signRequest(req, c.accessKey, c.secretKey, c.region, "bedrock", time.Now().UTC())

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

	var text string
	for _, block := range result.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text, nil
}

// Capabilities returns the capabilities supported by Bedrock.
func (c *Client) Capabilities() []bridge.Capability {
	return []bridge.Capability{bridge.CapChat, bridge.CapCode}
}

// Provider returns the provider name.
func (c *Client) Provider() string { return "bedrock" }
