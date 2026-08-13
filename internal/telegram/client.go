package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	ID int64 `json:"id"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *Message `json:"message"`
	From    User     `json:"from"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type InlineButton struct {
	Text         string
	CallbackData string
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: 65 * time.Second}}
}

func (c *Client) call(ctx context.Context, method string, body any, out any) error {
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+method, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("telegram %s failed: %s", method, envelope.Description)
	}
	if out != nil && len(envelope.Result) > 0 {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func replyMarkup(buttons [][]InlineButton) map[string]any {
	if len(buttons) == 0 {
		return nil
	}
	rows := make([][]map[string]string, 0, len(buttons))
	for _, row := range buttons {
		cells := make([]map[string]string, 0, len(row))
		for _, b := range row {
			cells = append(cells, map[string]string{"text": b.Text, "callback_data": b.CallbackData})
		}
		rows = append(rows, cells)
	}
	return map[string]any{"inline_keyboard": rows}
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": timeoutSec}, &updates)
	return updates, err
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, buttons [][]InlineButton) (int64, error) {
	body := map[string]any{"chat_id": chatID, "text": text}
	if rm := replyMarkup(buttons); rm != nil {
		body["reply_markup"] = rm
	}
	var out Message
	if err := c.call(ctx, "sendMessage", body, &out); err != nil {
		return 0, err
	}
	return out.MessageID, nil
}

func (c *Client) EditMessageText(ctx context.Context, chatID, messageID int64, text string, buttons [][]InlineButton) error {
	body := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text}
	if rm := replyMarkup(buttons); rm != nil {
		body["reply_markup"] = rm
	}
	return c.call(ctx, "editMessageText", body, nil)
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": callbackID, "text": text}, nil)
}
