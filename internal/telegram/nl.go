package telegram

import (
	"context"
	"encoding/json"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
)

type Intent struct {
	Action string
	Args   map[string]string
}

type NLParser interface {
	Parse(ctx context.Context, text string) (Intent, error)
}

type ClaudeNLParser struct {
	client anthropic.Client
}

func NewClaudeNLParser() (*ClaudeNLParser, bool) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil, false
	}
	return &ClaudeNLParser{client: anthropic.NewClient()}, true
}

const nlSystem = "You translate a user's chat message into a single Waggle bot action by calling the `route` tool. " +
	"Choose the closest action. If unclear, use action \"help\"."

func routeTool() anthropic.ToolUnionParam {
	tool := anthropic.ToolParam{
		Name:        "route",
		Description: anthropic.String("Route the user's message to a Waggle action."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"list_tasks", "create_task", "whats_next", "move_task", "help"},
				},
				"title":   map[string]any{"type": "string"},
				"task_id": map[string]any{"type": "string"},
				"status":  map[string]any{"type": "string"},
				"project": map[string]any{"type": "string"},
			},
			Required: []string{"action"},
		},
	}
	return anthropic.ToolUnionParam{OfTool: &tool}
}

func (p *ClaudeNLParser) Parse(ctx context.Context, text string) (Intent, error) {
	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5_20251001,
		MaxTokens: 1024,
		System:    []anthropic.TextBlockParam{{Text: nlSystem}},
		Tools:     []anthropic.ToolUnionParam{routeTool()},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(text))},
	})
	if err != nil {
		return Intent{}, err
	}
	for _, block := range resp.Content {
		if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			var raw map[string]any
			if err := json.Unmarshal(tu.Input, &raw); err != nil {
				return Intent{Action: "help"}, nil
			}
			intent := Intent{Args: map[string]string{}}
			for k, v := range raw {
				s, _ := v.(string)
				if k == "action" {
					intent.Action = s
				} else if s != "" {
					intent.Args[k] = s
				}
			}
			if intent.Action == "" {
				intent.Action = "help"
			}
			return intent, nil
		}
	}
	return Intent{Action: "help"}, nil
}
