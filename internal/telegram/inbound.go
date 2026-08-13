package telegram

import "context"

type Router struct {
	cfg Config
	h   *Handler
	nl  NLParser
}

func NewRouter(cfg Config, h *Handler, nl NLParser) *Router {
	return &Router{cfg: cfg, h: h, nl: nl}
}

func (r *Router) Dispatch(ctx context.Context, u Update) {
	var chatID int64
	switch {
	case u.CallbackQuery != nil && u.CallbackQuery.Message != nil:
		chatID = u.CallbackQuery.Message.Chat.ID
	case u.Message != nil:
		chatID = u.Message.Chat.ID
	default:
		return
	}
	if !r.cfg.ChatAllowed(chatID) {
		return
	}
	if u.CallbackQuery != nil {
		r.h.HandleCallback(ctx, u.CallbackQuery)
		return
	}
	text := u.Message.Text
	if len(text) > 0 && text[0] == '/' {
		r.h.HandleCommand(ctx, chatID, text)
		return
	}
	if r.nl == nil {
		r.h.tg.SendMessage(ctx, chatID, helpText, nil)
		return
	}
	intent, err := r.nl.Parse(ctx, text)
	if err != nil {
		r.h.tg.SendMessage(ctx, chatID, helpText, nil)
		return
	}
	r.dispatchIntent(ctx, chatID, intent)
}

func (r *Router) dispatchIntent(ctx context.Context, chatID int64, in Intent) {
	switch in.Action {
	case "list_tasks":
		r.h.HandleCommand(ctx, chatID, "/tasks "+in.Args["project"])
	case "create_task":
		r.h.HandleCommand(ctx, chatID, "/create "+in.Args["title"])
	case "whats_next":
		r.h.HandleCommand(ctx, chatID, "/next")
	case "move_task":
		if r.h.suppressor != nil {
			r.h.suppressor.Suppress(in.Args["task_id"])
		}
		if err := r.h.wg.MoveTask(in.Args["task_id"], in.Args["status"]); err != nil {
			r.h.tg.SendMessage(ctx, chatID, "error: "+err.Error(), nil)
			return
		}
		r.h.tg.SendMessage(ctx, chatID, "Moved to "+in.Args["status"], nil)
	default:
		r.h.tg.SendMessage(ctx, chatID, helpText, nil)
	}
}
