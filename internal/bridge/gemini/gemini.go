package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/maniginam/waggle/internal/bridge"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

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

	var contents []map[string]any
	for _, m := range messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "system" {
			continue // handled via system_instruction
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]any{{"text": m.Content}},
		})
	}

	reqBody := map[string]any{
		"contents": contents,
	}

	if opts.SystemPrompt != "" {
		reqBody["system_instruction"] = map[string]any{
			"parts": []map[string]any{{"text": opts.SystemPrompt}},
		}
	}

	if opts.MaxTokens > 0 || opts.Temperature > 0 {
		genConfig := map[string]any{}
		if opts.MaxTokens > 0 {
			genConfig["maxOutputTokens"] = opts.MaxTokens
		}
		if opts.Temperature > 0 {
			genConfig["temperature"] = opts.Temperature
		}
		reqBody["generationConfig"] = genConfig
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", c.baseURL, model, c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

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
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no candidates in response")
	}

	return result.Candidates[0].Content.Parts[0].Text, nil
}

func (c *Client) Capabilities() []bridge.Capability {
	return []bridge.Capability{bridge.CapChat, bridge.CapCode}
}

func (c *Client) Provider() string { return "gemini" }
