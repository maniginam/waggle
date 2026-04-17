package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type RunnerConfig struct {
	AgentName         string
	BaseURL           string
	Mode              Mode
	ProjectID         string
	SystemPrompt      string
	HeartbeatInterval time.Duration
	PollInterval      time.Duration
	TaskInterval      time.Duration
}

type Runner struct {
	bridge  Bridge
	config  RunnerConfig
	http    *http.Client
	history []Message
	working bool
}

func NewRunner(b Bridge, cfg RunnerConfig) *Runner {
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.TaskInterval == 0 {
		cfg.TaskInterval = 5 * time.Second
	}
	return &Runner{
		bridge: b,
		config: cfg,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *Runner) Run(ctx context.Context) {
	if err := r.register(); err != nil {
		log.Printf("bridge/%s: register failed: %v", r.config.AgentName, err)
		return
	}
	log.Printf("bridge/%s: registered as %s (%s mode)", r.config.AgentName, r.bridge.Provider(), r.config.Mode)

	heartbeat := time.NewTicker(r.config.HeartbeatInterval)
	poll := time.NewTicker(r.config.PollInterval)
	defer heartbeat.Stop()
	defer poll.Stop()

	var taskTicker *time.Ticker
	if r.config.Mode == ModeFullParticipant {
		taskTicker = time.NewTicker(r.config.TaskInterval)
		defer taskTicker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			r.disconnect()
			return
		case <-heartbeat.C:
			r.sendHeartbeat()
		case <-poll.C:
			r.pollMessages(ctx)
		case <-tickerChan(taskTicker):
			r.pollTasks(ctx)
		}
	}
}

func tickerChan(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func (r *Runner) register() error {
	body := map[string]string{
		"name":       r.config.AgentName,
		"type":       r.bridge.Provider(),
		"project_id": r.config.ProjectID,
	}
	_, err := r.post("/api/agents/register", body)
	return err
}

func (r *Runner) sendHeartbeat() {
	r.post("/api/agents/"+r.config.AgentName+"/heartbeat", nil)
}

func (r *Runner) disconnect() {
	r.post("/api/agents/"+r.config.AgentName+"/status", map[string]string{"status": "disconnected"})
	log.Printf("bridge/%s: disconnected", r.config.AgentName)
}

type apiMessage struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Body string `json:"body"`
	Read bool   `json:"read"`
}

func (r *Runner) pollMessages(ctx context.Context) {
	resp, err := r.get(fmt.Sprintf("/api/messages?to=%s&limit=10", r.config.AgentName))
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var msgs []apiMessage
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return
	}

	for _, msg := range msgs {
		if msg.Read {
			continue
		}
		r.handleMessage(ctx, msg)
	}
}

func (r *Runner) handleMessage(ctx context.Context, msg apiMessage) {
	r.history = append(r.history, Message{Role: "user", Content: msg.Body})

	opts := ChatOpts{SystemPrompt: r.config.SystemPrompt}
	response, err := r.bridge.Chat(ctx, r.history, opts)
	if err != nil {
		log.Printf("bridge/%s: chat error: %v", r.config.AgentName, err)
		response = fmt.Sprintf("[bridge error: %v]", err)
	}

	r.history = append(r.history, Message{Role: "assistant", Content: response})

	r.post("/api/messages", map[string]string{
		"from": r.config.AgentName,
		"to":   msg.From,
		"body": response,
	})

	r.patch("/api/messages", map[string]any{
		"ids": []string{msg.ID},
	})
}

type apiTask struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Criteria    []string `json:"criteria"`
}

func (r *Runner) pollTasks(ctx context.Context) {
	if r.working {
		return
	}

	query := "/api/tasks?status=ready"
	if r.config.ProjectID != "" {
		query += "&project_id=" + r.config.ProjectID
	}
	resp, err := r.get(query)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var tasks []apiTask
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil || len(tasks) == 0 {
		return
	}

	r.claimAndWork(ctx, tasks[0])
}

func (r *Runner) claimAndWork(ctx context.Context, task apiTask) {
	_, err := r.post("/api/tasks/"+task.ID+"/claim", map[string]string{"agent": r.config.AgentName})
	if err != nil {
		return
	}
	r.working = true
	defer func() { r.working = false }()

	prompt := fmt.Sprintf("Task: %s\n", task.Title)
	if task.Description != "" {
		prompt += fmt.Sprintf("Description: %s\n", task.Description)
	}
	if len(task.Criteria) > 0 {
		prompt += "Acceptance Criteria:\n"
		for _, c := range task.Criteria {
			prompt += fmt.Sprintf("- %s\n", c)
		}
	}

	msgs := []Message{{Role: "user", Content: prompt}}
	opts := ChatOpts{SystemPrompt: r.config.SystemPrompt}

	response, err := r.bridge.Chat(ctx, msgs, opts)
	if err != nil {
		log.Printf("bridge/%s: task chat error: %v", r.config.AgentName, err)
		return
	}

	r.post("/api/tasks/"+task.ID+"/comments", map[string]string{
		"author": r.config.AgentName,
		"body":   response,
	})

	r.post("/api/tasks/"+task.ID+"/complete", map[string]string{"agent": r.config.AgentName})
}

func (r *Runner) post(path string, body any) (*http.Response, error) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	resp, err := r.http.Post(r.config.BaseURL+path, "application/json", &buf)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("POST %s: status %d: %s", path, resp.StatusCode, string(respBody))
	}
	return resp, nil
}

func (r *Runner) get(path string) (*http.Response, error) {
	return r.http.Get(r.config.BaseURL + path)
}

func (r *Runner) patch(path string, body any) {
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req, err := http.NewRequest(http.MethodPatch, r.config.BaseURL+path, &buf)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	r.http.Do(req)
}
