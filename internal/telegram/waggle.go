package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type WaggleClient struct {
	baseURL string
	http    *http.Client
}

func NewWaggleClient(baseURL string) *WaggleClient {
	return &WaggleClient{baseURL: baseURL, http: &http.Client{Timeout: 10 * time.Second}}
}

func (w *WaggleClient) get(path string) ([]byte, error) {
	resp, err := w.http.Get(w.baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s: %s", path, string(body))
	}
	return body, nil
}

func (w *WaggleClient) post(path string, payload any) ([]byte, error) {
	var reader io.Reader
	if payload != nil {
		data, _ := json.Marshal(payload)
		reader = bytes.NewReader(data)
	}
	resp, err := w.http.Post(w.baseURL+path, "application/json", reader)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("POST %s: %s", path, string(body))
	}
	return body, nil
}

func (w *WaggleClient) ListTasks(projectID string) ([]map[string]any, error) {
	path := "/api/tasks"
	if projectID != "" {
		path += "?project_id=" + url.QueryEscape(projectID)
	}
	body, err := w.get(path)
	if err != nil {
		return nil, err
	}
	var tasks []map[string]any
	if err := json.Unmarshal(body, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (w *WaggleClient) MoveTask(taskID, status string) error {
	_, err := w.post("/api/tasks/"+url.PathEscape(taskID)+"/move", map[string]any{"status": status})
	return err
}

func (w *WaggleClient) WhatsNext() ([]byte, error) {
	return w.get("/api/whats-next")
}

func (w *WaggleClient) CreateTask(title, projectID string) (map[string]any, error) {
	body, err := w.post("/api/tasks", map[string]any{"title": title, "project_id": projectID})
	if err != nil {
		return nil, err
	}
	var task map[string]any
	if err := json.Unmarshal(body, &task); err != nil {
		return nil, err
	}
	return task, nil
}
