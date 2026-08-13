package telegram

import (
	"context"
	"fmt"
	"strings"
)

type Handler struct {
	tg *Client
	wg *WaggleClient
}

func NewHandler(tg *Client, wg *WaggleClient) *Handler {
	return &Handler{tg: tg, wg: wg}
}

const helpText = "Waggle bot commands:\n" +
	"/next - what needs attention across projects\n" +
	"/tasks [project] - list tasks with action buttons\n" +
	"/create <title> - create a task\n" +
	"/help - this message"

func moveButtons(taskID string) [][]InlineButton {
	return [][]InlineButton{{
		{Text: "In Progress", CallbackData: "mv:" + taskID + ":in_progress"},
		{Text: "Review", CallbackData: "mv:" + taskID + ":review"},
		{Text: "Done", CallbackData: "mv:" + taskID + ":done"},
	}}
}

func (h *Handler) HandleCommand(ctx context.Context, chatID int64, text string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return
	}
	cmd := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(text, cmd))
	switch cmd {
	case "/next":
		body, err := h.wg.WhatsNext()
		if err != nil {
			h.tg.SendMessage(ctx, chatID, "error: "+err.Error(), nil)
			return
		}
		h.tg.SendMessage(ctx, chatID, "What's next:\n"+string(body), nil)
	case "/tasks":
		tasks, err := h.wg.ListTasks(arg)
		if err != nil {
			h.tg.SendMessage(ctx, chatID, "error: "+err.Error(), nil)
			return
		}
		if len(tasks) == 0 {
			h.tg.SendMessage(ctx, chatID, "No tasks.", nil)
			return
		}
		for _, task := range tasks {
			id, _ := task["id"].(string)
			title, _ := task["title"].(string)
			status, _ := task["status"].(string)
			h.tg.SendMessage(ctx, chatID, fmt.Sprintf("%s [%s]", title, status), moveButtons(id))
		}
	case "/create":
		if arg == "" {
			h.tg.SendMessage(ctx, chatID, "usage: /create <title>", nil)
			return
		}
		task, err := h.wg.CreateTask(arg, "")
		if err != nil {
			h.tg.SendMessage(ctx, chatID, "error: "+err.Error(), nil)
			return
		}
		h.tg.SendMessage(ctx, chatID, "Created: "+task["title"].(string), nil)
	default:
		h.tg.SendMessage(ctx, chatID, helpText, nil)
	}
}

func (h *Handler) HandleCallback(ctx context.Context, cb *CallbackQuery) {
	parts := strings.SplitN(cb.Data, ":", 3)
	if len(parts) != 3 || parts[0] != "mv" {
		h.tg.AnswerCallbackQuery(ctx, cb.ID, "unknown action")
		return
	}
	taskID, status := parts[1], parts[2]
	if err := h.wg.MoveTask(taskID, status); err != nil {
		h.tg.AnswerCallbackQuery(ctx, cb.ID, "error")
		return
	}
	if cb.Message != nil {
		h.tg.EditMessageText(ctx, cb.Message.Chat.ID, cb.Message.MessageID,
			fmt.Sprintf("Moved to %s", status), nil)
	}
	h.tg.AnswerCallbackQuery(ctx, cb.ID, "Moved to "+status)
}
