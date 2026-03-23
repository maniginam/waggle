package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/internal/store"
)

type API struct {
	store    *store.Store
	eventHub *event.Hub
}

func New(s *store.Store, eh *event.Hub) *API {
	return &API{store: s, eventHub: eh}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks", a.handleTasks)
	mux.HandleFunc("/api/tasks/", a.handleTask)
	mux.HandleFunc("/api/agents", a.handleAgents)
	mux.HandleFunc("/api/agents/", a.handleAgent)
	mux.HandleFunc("/api/events", a.handleEvents)
	mux.HandleFunc("/api/messages", a.handleMessages)
	mux.HandleFunc("/api/stats", a.handleStats)
	return mux
}

func (a *API) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		filters := map[string]string{}
		for _, key := range []string{"status", "assignee", "priority", "tag", "parent_id", "q", "sort", "order"} {
			if v := r.URL.Query().Get(key); v != "" {
				filters[key] = v
			}
		}
		tasks, err := a.store.ListTasks(filters)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		if tasks == nil {
			tasks = []*model.Task{}
		}
		writeJSON(w, http.StatusOK, tasks)

	case http.MethodPost:
		var task model.Task
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if task.Title == "" {
			writeError(w, http.StatusBadRequest, "missing_title", "title is required")
			return
		}
		if task.Status != "" && !task.Status.Valid() {
			writeError(w, http.StatusBadRequest, "invalid_status", "invalid status: "+string(task.Status))
			return
		}
		if task.Priority != "" && !task.Priority.Valid() {
			writeError(w, http.StatusBadRequest, "invalid_priority", "invalid priority: "+string(task.Priority))
			return
		}
		if err := a.store.CreateTask(&task); err != nil {
			if err == store.ErrCycleDep {
				writeError(w, http.StatusBadRequest, "cycle_detected", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
			return
		}
		a.store.RecordEvent(&model.Event{Type: model.EventTaskCreated, TaskID: task.ID, Payload: task})
		a.eventHub.Publish(&model.Event{Type: model.EventTaskCreated, TaskID: task.ID, Payload: task})
		writeJSON(w, http.StatusCreated, task)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleTask(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "task ID required")
		return
	}

	// Handle sub-paths like /api/tasks/:id/claim
	parts := strings.SplitN(id, "/", 2)
	id = parts[0]
	subAction := ""
	if len(parts) > 1 {
		subAction = parts[1]
	}

	switch subAction {
	case "claim":
		a.handleTaskClaim(w, r, id)
		return
	case "unclaim":
		a.handleTaskUnclaim(w, r, id)
		return
	case "complete":
		a.handleTaskComplete(w, r, id)
		return
	case "comments":
		a.handleTaskComments(w, r, id)
		return
	case "history":
		a.handleTaskHistory(w, r, id)
		return
	case "subtasks":
		a.handleSubtasks(w, r, id)
		return
	case "deps":
		a.handleTaskDeps(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		task, err := a.store.GetTask(id)
		if err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "task_not_found", "Task "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, task)

	case http.MethodPatch:
		var updates map[string]any
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		task, err := a.store.UpdateTask(id, updates)
		if err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "task_not_found", "Task "+id+" not found")
				return
			}
			if err == store.ErrCycleDep {
				writeError(w, http.StatusBadRequest, "cycle_detected", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
			return
		}
		a.store.RecordEvent(&model.Event{Type: model.EventTaskUpdated, TaskID: id, Payload: updates})
		a.eventHub.Publish(&model.Event{Type: model.EventTaskUpdated, TaskID: id, Payload: updates})
		writeJSON(w, http.StatusOK, task)

	case http.MethodDelete:
		if err := a.store.DeleteTask(id); err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "task_not_found", "Task "+id+" not found")
				return
			}
			if err == store.ErrInProgress {
				writeError(w, http.StatusConflict, "task_in_progress", "Cannot delete in-progress task")
				return
			}
			writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
			return
		}
		a.store.RecordEvent(&model.Event{Type: model.EventTaskDeleted, TaskID: id})
		a.eventHub.Publish(&model.Event{Type: model.EventTaskDeleted, TaskID: id})
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleTaskClaim(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Agent string `json:"agent"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Agent == "" {
		writeError(w, http.StatusBadRequest, "missing_agent", "agent name required")
		return
	}
	if err := a.store.ClaimTask(taskID, req.Agent); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "task_not_found", "Task "+taskID+" not found")
			return
		}
		if err == store.ErrAlreadyClaimed {
			writeError(w, http.StatusConflict, "already_claimed", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "claim_failed", err.Error())
		return
	}
	a.store.RecordEvent(&model.Event{Type: model.EventTaskClaimed, AgentID: req.Agent, TaskID: taskID})
	a.eventHub.Publish(&model.Event{Type: model.EventTaskClaimed, AgentID: req.Agent, TaskID: taskID})
	task, _ := a.store.GetTask(taskID)
	writeJSON(w, http.StatusOK, task)
}

func (a *API) handleTaskUnclaim(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Agent string `json:"agent"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Agent == "" {
		writeError(w, http.StatusBadRequest, "missing_agent", "agent name required")
		return
	}
	if err := a.store.UnclaimTask(taskID, req.Agent); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "task_not_found", "Task "+taskID+" not found")
			return
		}
		if err == store.ErrNotAssigned {
			writeError(w, http.StatusForbidden, "not_assigned", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "unclaim_failed", err.Error())
		return
	}
	a.store.RecordEvent(&model.Event{Type: model.EventTaskUnclaimed, AgentID: req.Agent, TaskID: taskID})
	a.eventHub.Publish(&model.Event{Type: model.EventTaskUnclaimed, AgentID: req.Agent, TaskID: taskID})
	task, _ := a.store.GetTask(taskID)
	writeJSON(w, http.StatusOK, task)
}

func (a *API) handleTaskComplete(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := a.store.CompleteTask(taskID); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "task_not_found", "Task "+taskID+" not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "complete_failed", err.Error())
		return
	}
	a.store.RecordEvent(&model.Event{Type: model.EventTaskCompleted, TaskID: taskID})
	a.eventHub.Publish(&model.Event{Type: model.EventTaskCompleted, TaskID: taskID})
	task, _ := a.store.GetTask(taskID)
	writeJSON(w, http.StatusOK, task)
}

func (a *API) handleTaskComments(w http.ResponseWriter, r *http.Request, taskID string) {
	switch r.Method {
	case http.MethodGet:
		comments, err := a.store.ListComments(taskID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		if comments == nil {
			comments = []*model.Comment{}
		}
		writeJSON(w, http.StatusOK, comments)

	case http.MethodPost:
		var c model.Comment
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if c.Author == "" || c.Body == "" {
			writeError(w, http.StatusBadRequest, "missing_fields", "author and body are required")
			return
		}
		c.TaskID = taskID
		if err := a.store.AddComment(&c); err != nil {
			writeError(w, http.StatusInternalServerError, "comment_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, &c)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleTaskHistory(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	events, err := a.store.ListTaskEvents(taskID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if events == nil {
		events = []*model.Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (a *API) handleSubtasks(w http.ResponseWriter, r *http.Request, parentID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	tasks, err := a.store.ListSubtasks(parentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if tasks == nil {
		tasks = []*model.Task{}
	}

	done, total, _ := a.store.SubtaskProgress(parentID)
	writeJSON(w, http.StatusOK, map[string]any{
		"subtasks": tasks,
		"progress": map[string]int{"done": done, "total": total},
	})
}

func (a *API) handleTaskDeps(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	dependsOn, blockedBy, err := a.store.TaskDeps(taskID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "task_not_found", "Task "+taskID+" not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "deps_failed", err.Error())
		return
	}
	if dependsOn == nil {
		dependsOn = []*model.Task{}
	}
	if blockedBy == nil {
		blockedBy = []*model.Task{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"depends_on":  dependsOn,
		"blocking":    blockedBy,
	})
}

func (a *API) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	statusFilter := r.URL.Query().Get("status")
	agents, err := a.store.ListAgents(statusFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if agents == nil {
		agents = []*model.Agent{}
	}
	writeJSON(w, http.StatusOK, agents)
}

func (a *API) handleAgent(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]
	subAction := ""
	if len(parts) > 1 {
		subAction = parts[1]
	}

	// POST /api/agents/register
	if name == "register" && r.Method == http.MethodPost {
		var req struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, "missing_name", "agent name required")
			return
		}
		if req.Type == "" {
			req.Type = "custom"
		}
		agent, err := a.store.RegisterAgent(req.Name, req.Type)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "register_failed", err.Error())
			return
		}
		a.store.RecordEvent(&model.Event{Type: model.EventAgentJoined, AgentID: agent.Name, Payload: agent})
		a.eventHub.Publish(&model.Event{Type: model.EventAgentJoined, AgentID: agent.Name, Payload: agent})
		writeJSON(w, http.StatusOK, agent)
		return
	}

	// POST /api/agents/:name/status
	if subAction == "status" && r.Method == http.MethodPost {
		var req struct {
			Status      string `json:"status"`
			CurrentTask string `json:"current_task"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if err := a.store.UpdateAgentStatus(name, model.AgentStatus(req.Status), req.CurrentTask); err != nil {
			writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
			return
		}
		a.store.RecordEvent(&model.Event{Type: model.EventAgentStatusChanged, AgentID: name, Payload: req})
		a.eventHub.Publish(&model.Event{Type: model.EventAgentStatusChanged, AgentID: name, Payload: req})
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
		return
	}

	// GET /api/agents/:id
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	agent, err := a.store.GetAgent(name)
	if err != nil {
		// Try by name if not found by ID
		if err == store.ErrNotFound {
			agent, err = a.store.GetAgentByName(name)
		}
		if err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "agent_not_found", "Agent "+name+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, agent)
}

func (a *API) handleMessages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		to := r.URL.Query().Get("to")
		if to == "" {
			writeError(w, http.StatusBadRequest, "missing_to", "'to' query param required")
			return
		}
		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			fmt.Sscanf(l, "%d", &limit)
		}
		msgs, err := a.store.ReadMessages(to, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read_failed", err.Error())
			return
		}
		if msgs == nil {
			msgs = []*model.Message{}
		}
		writeJSON(w, http.StatusOK, msgs)

	case http.MethodPost:
		var msg model.Message
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if msg.From == "" || msg.Body == "" {
			writeError(w, http.StatusBadRequest, "missing_fields", "from and body are required")
			return
		}
		if err := a.store.SendMessage(&msg); err != nil {
			writeError(w, http.StatusInternalServerError, "send_failed", err.Error())
			return
		}
		a.store.RecordEvent(&model.Event{Type: model.EventMessage, AgentID: msg.From, Payload: msg})
		a.eventHub.Publish(&model.Event{Type: model.EventMessage, AgentID: msg.From, Payload: msg})
		writeJSON(w, http.StatusCreated, msg)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Check for SSE
	if r.Header.Get("Accept") == "text/event-stream" {
		a.handleSSE(w, r)
		return
	}

	events, err := a.store.ListEvents(50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if events == nil {
		events = []*model.Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (a *API) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	agentFilter := r.URL.Query().Get("agent")
	taskFilter := r.URL.Query().Get("task")
	sub := a.eventHub.Subscribe(agentFilter, taskFilter)
	defer a.eventHub.Unsubscribe(sub)

	for {
		select {
		case evt, ok := <-sub.Ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(evt)
			w.Write([]byte("data: "))
			w.Write(data)
			w.Write([]byte("\n\n"))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	stats, err := a.store.Stats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
