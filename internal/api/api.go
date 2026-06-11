package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/internal/store"
)

const maxBodySize = 1 << 20 // 1MB

var safeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

type API struct {
	store       *store.Store
	eventHub    *event.Hub
	rateLimiter *rateLimiter
	procsMu    sync.Mutex
	procs      map[string]*exec.Cmd    // spawned agent processes by name
	procsStdin map[string]io.WriteCloser // stdin pipes for spawned agents
	agentDirs      map[string]string        // last-known work_dir per agent name
}

func New(s *store.Store, eh *event.Hub) *API {
	return &API{
		store:       s,
		eventHub:    eh,
		rateLimiter: newRateLimiter(120, time.Minute),
		procs:       make(map[string]*exec.Cmd),
		procsStdin:  make(map[string]io.WriteCloser),
		agentDirs:   make(map[string]string),
	}
}

// emit records an event to the store (which assigns its ID) then publishes it
// to the event hub so SSE clients receive events with proper IDs for replay.
func (a *API) emit(evt *model.Event) {
	a.store.RecordEvent(evt)
	a.eventHub.Publish(evt)
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks", a.handleTasks)
	mux.HandleFunc("/api/tasks/", a.handleTask)
	mux.HandleFunc("/api/projects", a.handleProjects)
	mux.HandleFunc("/api/projects/", a.handleProject)
	mux.HandleFunc("/api/agents", a.handleAgents)
	mux.HandleFunc("/api/agents/", a.handleAgent)
	mux.HandleFunc("/api/events", a.handleEvents)
	mux.HandleFunc("/api/messages", a.handleMessages)
	mux.HandleFunc("/api/git", a.handleGitOverview)
	mux.HandleFunc("/api/spawn", a.handleSpawn)
	mux.HandleFunc("/api/sessions", a.handleSessions)
	mux.HandleFunc("/api/sessions/", a.handleSessionAction)
	mux.HandleFunc("/api/push/subscribe", a.handlePushSubscribe)
	mux.HandleFunc("/api/settings", a.handleSettings)
	mux.HandleFunc("/api/alerts", a.handleAlerts)
	// Context manager endpoints
	mux.HandleFunc("/api/progress", a.handleProgress)
	mux.HandleFunc("/api/ctx-sessions", a.handleCtxSessions)
	mux.HandleFunc("/api/ctx-sessions/", a.handleCtxSessionAction)
	mux.HandleFunc("/api/briefing", a.handleBriefing)
	mux.HandleFunc("/api/whats-next", a.handleWhatsNext)
	mux.HandleFunc("/api/health-check", a.handleHealthCheck)
	// Middleware chain: rate limit → body size limit → request log → route
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Rate limiting (per-IP, 120 req/min for writes, unlimited reads)
		if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			ip := r.RemoteAddr
			if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
				ip = strings.Split(fwd, ",")[0]
			}
			if !a.rateLimiter.Allow(ip) {
				writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests, try again later")
				return
			}
		}
		// Body size limit
		if r.Method == http.MethodPost || r.Method == http.MethodPatch {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
		}
		// Request logging (skip SSE and high-frequency endpoints)
		if r.URL.Path != "/api/events" && !strings.HasSuffix(r.URL.Path, "/heartbeat") {
			start := time.Now()
			rw := &statusWriter{ResponseWriter: w, status: 200}
			mux.ServeHTTP(rw, r)
			if rw.status >= 400 || time.Since(start) > 500*time.Millisecond {
				log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
			}
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// statusWriter captures the HTTP status code for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// rateLimiter implements a simple per-key token bucket.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    int           // tokens per window
	window  time.Duration // window duration
}

type bucket struct {
	tokens int
	last   time.Time
}

func newRateLimiter(rate int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		window:  window,
	}
}

func (rl *rateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	// Periodic cleanup of stale buckets (every 100 calls)
	if len(rl.buckets) > 100 {
		for k, b := range rl.buckets {
			if now.Sub(b.last) > rl.window*2 {
				delete(rl.buckets, k)
			}
		}
	}

	b, ok := rl.buckets[key]
	if !ok || now.Sub(b.last) > rl.window {
		rl.buckets[key] = &bucket{tokens: rl.rate - 1, last: now}
		return true
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

func (a *API) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		filters := map[string]string{}
		for _, key := range []string{"status", "assignee", "priority", "tag", "parent_id", "project_id", "task_type", "q", "sort", "order"} {
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
		if len(task.Title) > 500 {
			writeError(w, http.StatusBadRequest, "title_too_long", "title must be 500 characters or less")
			return
		}
		if len(task.Description) > 10000 {
			writeError(w, http.StatusBadRequest, "description_too_long", "description must be 10000 characters or less")
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
		a.emit(&model.Event{Type: model.EventTaskCreated, TaskID: task.ID, Payload: task})
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

	// Handle export endpoint: /api/tasks/export
	if id == "export" || strings.HasPrefix(id, "export?") {
		a.handleTaskExport(w, r)
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
		a.emit(&model.Event{Type: model.EventTaskUpdated, TaskID: id, Payload: updates})
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
		a.emit(&model.Event{Type: model.EventTaskDeleted, TaskID: id})
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
	a.emit(&model.Event{Type: model.EventTaskClaimed, AgentID: req.Agent, TaskID: taskID})
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
	a.emit(&model.Event{Type: model.EventTaskUnclaimed, AgentID: req.Agent, TaskID: taskID})
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
	a.emit(&model.Event{Type: model.EventTaskCompleted, TaskID: taskID})
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
		if len(c.Body) > 5000 {
			writeError(w, http.StatusBadRequest, "body_too_long", "comment body max 5000 chars")
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
			Name        string          `json:"name"`
			Type        string          `json:"type"`
			ProjectID   string          `json:"project_id"`
			Role        model.AgentRole `json:"role"`
			ParentAgent string          `json:"parent_agent"`
			PersonaID   string          `json:"persona_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, "missing_name", "agent name required")
			return
		}
		if len(req.Name) > 64 {
			writeError(w, http.StatusBadRequest, "name_too_long", "agent name max 64 chars")
			return
		}
		if req.Type == "" {
			req.Type = "custom"
		}
		// Auto-assign leader role if project has no leader yet
		if req.Role == "" && req.ProjectID != "" {
			proj, _ := a.store.GetProject(req.ProjectID)
			if proj != nil && proj.LeaderAgent == "" {
				req.Role = model.AgentRoleLeader
			}
		}
		agent, err := a.store.RegisterAgent(req.Name, req.Type, req.ProjectID, req.Role, req.ParentAgent)
		// If registered as leader, update the project
		if err == nil && agent.Role == model.AgentRoleLeader && req.ProjectID != "" {
			a.store.UpdateProject(req.ProjectID, map[string]any{"leader_agent": agent.Name})
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "register_failed", err.Error())
			return
		}
		a.emit(&model.Event{Type: model.EventAgentJoined, AgentID: agent.Name, Payload: agent})
		writeJSON(w, http.StatusOK, agent)
		return
	}

	// POST /api/agents/:name/heartbeat
	if subAction == "heartbeat" && r.Method == http.MethodPost {
		if err := a.store.TouchAgent(name); err != nil {
			writeError(w, http.StatusInternalServerError, "heartbeat_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
		if req.Status == string(model.AgentDisconnected) {
			if err := a.store.DisconnectAgent(name); err != nil {
				writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
				return
			}
			a.emit(&model.Event{Type: model.EventAgentLeft, AgentID: name})
		} else {
			if err := a.store.UpdateAgentStatus(name, model.AgentStatus(req.Status), req.CurrentTask); err != nil {
				writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
				return
			}
			a.emit(&model.Event{Type: model.EventAgentStatusChanged, AgentID: name, Payload: req})

			// Auto-dispatch when agent goes idle or connected (no current task)
			if req.Status == "idle" || req.Status == "connected" {
				if agent, err := a.store.GetAgentByName(name); err == nil && agent.ProjectID != "" {
					a.tryAutoDispatch(name, agent.ProjectID)
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
		return
	}

	// POST /api/agents/:name/project
	if subAction == "project" && r.Method == http.MethodPost {
		var req struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if err := a.store.UpdateAgentProject(name, req.ProjectID); err != nil {
			writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
		return
	}

	// POST /api/agents/:name/respawn
	if subAction == "respawn" && r.Method == http.MethodPost {
		// Look up the agent
		agent, err := a.store.GetAgentByName(name)
		if err != nil {
			writeError(w, http.StatusNotFound, "agent_not_found", "Agent "+name+" not found")
			return
		}

		var req struct {
			WorkDir string `json:"work_dir"`
			Prompt  string `json:"prompt"`
			Model   string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		// Disconnect the old agent record
		a.store.DisconnectAgent(name)

		if req.WorkDir == "" {
			writeError(w, http.StatusBadRequest, "missing_work_dir", "work_dir is required for respawn")
			return
		}
		if _, err := os.Stat(req.WorkDir); os.IsNotExist(err) {
			writeError(w, http.StatusBadRequest, "invalid_work_dir", "directory does not exist: "+req.WorkDir)
			return
		}

		// Re-spawn via the existing spawn endpoint by synthesizing a request
		spawnBody, _ := json.Marshal(map[string]string{
			"name":       name,
			"project_id": agent.ProjectID,
			"work_dir":   req.WorkDir,
			"prompt":     req.Prompt,
			"model":      req.Model,
		})
		spawnReq, _ := http.NewRequest(http.MethodPost, "/api/spawn", bytes.NewBuffer(spawnBody))
		spawnReq.Header.Set("Content-Type", "application/json")
		a.handleSpawn(w, spawnReq)
		return
	}

	// DELETE /api/agents/:name
	if r.Method == http.MethodDelete && subAction == "" {
		if err := a.store.DeleteAgent(name); err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "agent_not_found", "agent not found: "+name)
				return
			}
			writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
			return
		}
		a.emit(&model.Event{Type: model.EventAgentLeft, AgentID: name})
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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
		agent := r.URL.Query().Get("agent")
		q := r.URL.Query().Get("q")
		projectID := r.URL.Query().Get("project_id")
		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			fmt.Sscanf(l, "%d", &limit)
		}
		var msgs []*model.Message
		var err error
		if q != "" {
			msgs, err = a.store.SearchMessages(q, limit)
		} else if projectID != "" {
			msgs, err = a.store.ProjectMessages(projectID, limit)
		} else if agent != "" {
			msgs, err = a.store.AgentMessages(agent, limit)
		} else if to == "" {
			msgs, err = a.store.ListAllMessages(limit)
		} else {
			msgs, err = a.store.ReadMessages(to, limit)
		}
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
		if len(msg.Body) > 10000 {
			writeError(w, http.StatusBadRequest, "body_too_long", "message body max 10000 chars")
			return
		}
		if len(msg.From) > 64 || len(msg.To) > 64 {
			writeError(w, http.StatusBadRequest, "name_too_long", "from/to max 64 chars")
			return
		}
		if err := a.store.SendMessage(&msg); err != nil {
			writeError(w, http.StatusInternalServerError, "send_failed", err.Error())
			return
		}
		a.emit(&model.Event{Type: model.EventMessage, AgentID: msg.From, Payload: msg})

		// If message targets a specific agent, wake them up
		if msg.To != "" && msg.To != "user" && msg.From == "user" {
			a.wakeAgent(msg.To, msg.Body)
		}

		writeJSON(w, http.StatusCreated, msg)

	case http.MethodPatch:
		var req struct {
			IDs     []string `json:"ids"`
			MarkAll bool     `json:"mark_all"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		var err error
		if req.MarkAll {
			err = a.store.MarkAllMessagesRead()
		} else {
			err = a.store.MarkMessagesRead(req.IDs)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "mark_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})

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

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
		if limit <= 0 {
			limit = 50
		}
		if limit > 500 {
			limit = 500
		}
	}
	events, err := a.store.ListEvents(limit)
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

	// Replay missed events if client reconnects with Last-Event-ID (header or query param)
	lastID := r.Header.Get("Last-Event-ID")
	if lastID == "" {
		lastID = r.URL.Query().Get("lastEventId")
	}
	if lastID != "" {
		if missed, err := a.store.ListEventsSince(lastID, 200); err == nil {
			for _, evt := range missed {
				data, _ := json.Marshal(evt)
				fmt.Fprintf(w, "id: %s\ndata: %s\n\n", evt.ID, data)
			}
			flusher.Flush()
		}
	}

	sub := a.eventHub.Subscribe(agentFilter, taskFilter)
	defer a.eventHub.Unsubscribe(sub)

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case evt, ok := <-sub.Ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(evt)
			if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", evt.ID, data); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (a *API) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projects, err := a.store.ListProjects()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		if projects == nil {
			projects = []*model.Project{}
		}
		// Enrich with task counts if requested
		if r.URL.Query().Get("counts") == "true" {
			type projectWithCounts struct {
				*model.Project
				TaskCount   int `json:"task_count"`
				DoneCount   int `json:"done_count"`
				ActiveCount int `json:"active_count"`
			}
			enriched := make([]projectWithCounts, 0, len(projects))
			for _, p := range projects {
				pc := projectWithCounts{Project: p}
				if tasks, err := a.store.ListTasks(map[string]string{"project_id": p.ID}); err == nil {
					pc.TaskCount = len(tasks)
					for _, t := range tasks {
						if t.Status == model.TaskDone {
							pc.DoneCount++
						} else if t.Status == model.TaskInProgress {
							pc.ActiveCount++
						}
					}
				}
				enriched = append(enriched, pc)
			}
			writeJSON(w, http.StatusOK, enriched)
			return
		}
		writeJSON(w, http.StatusOK, projects)

	case http.MethodPost:
		var p model.Project
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if p.Name == "" {
			writeError(w, http.StatusBadRequest, "missing_name", "name is required")
			return
		}
		if len(p.Name) > 200 {
			writeError(w, http.StatusBadRequest, "name_too_long", "name must be 200 characters or less")
			return
		}
		if err := a.store.CreateProject(&p); err != nil {
			writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, p)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleProject(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "project ID required")
		return
	}

	// Handle /api/projects/:id/epics
	parts := strings.SplitN(id, "/", 2)
	id = parts[0]
	subAction := ""
	if len(parts) > 1 {
		subAction = parts[1]
	}

	if subAction == "epics" {
		a.handleProjectEpics(w, r, id)
		return
	}
	if subAction == "git" {
		a.handleProjectGit(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		project, err := a.store.GetProject(id)
		if err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "project_not_found", "Project "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "get_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, project)

	case http.MethodPatch:
		var updates map[string]any
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		project, err := a.store.UpdateProject(id, updates)
		if err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "project_not_found", "Project "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "update_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, project)

	case http.MethodDelete:
		if err := a.store.DeleteProject(id); err != nil {
			if err == store.ErrNotFound {
				writeError(w, http.StatusNotFound, "project_not_found", "Project "+id+" not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleProjectEpics(w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	tasks, err := a.store.ListTasks(map[string]string{
		"project_id": projectID,
		"task_type":  "epic",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	if tasks == nil {
		tasks = []*model.Task{}
	}

	// Enrich each epic with subtask progress
	type epicWithProgress struct {
		*model.Task
		Progress map[string]int `json:"progress"`
	}
	var result []epicWithProgress
	for _, t := range tasks {
		done, total, _ := a.store.SubtaskProgress(t.ID)
		result = append(result, epicWithProgress{
			Task:     t,
			Progress: map[string]int{"done": done, "total": total},
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleGitOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	projects, err := a.store.ListProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}

	type gitSummary struct {
		ProjectID   string `json:"project_id"`
		ProjectName string `json:"project_name"`
		WorkDir     string `json:"work_dir"`
		Branch      string `json:"branch"`
		Status      string `json:"status"`
		Ahead       string `json:"ahead"`
		Behind      string `json:"behind"`
		Dirty       bool   `json:"dirty"`
	}

	var results []gitSummary
	for _, p := range projects {
		if p.WorkDir == "" {
			continue
		}
		if _, err := os.Stat(p.WorkDir); os.IsNotExist(err) {
			continue
		}
		runGit := func(args ...string) string {
			cmd := exec.Command("git", args...)
			cmd.Dir = p.WorkDir
			out, _ := cmd.Output()
			return strings.TrimSpace(string(out))
		}
		branch := runGit("rev-parse", "--abbrev-ref", "HEAD")
		status := runGit("status", "--short")
		remote := runGit("rev-parse", "--abbrev-ref", "@{upstream}")
		ahead, behind := "0", "0"
		if remote != "" {
			ab := runGit("rev-list", "--left-right", "--count", remote+"...HEAD")
			parts := strings.Fields(ab)
			if len(parts) == 2 {
				behind, ahead = parts[0], parts[1]
			}
		}
		results = append(results, gitSummary{
			ProjectID:   p.ID,
			ProjectName: p.Name,
			WorkDir:     p.WorkDir,
			Branch:      branch,
			Status:      status,
			Ahead:       ahead,
			Behind:      behind,
			Dirty:       status != "",
		})
	}
	if results == nil {
		results = []gitSummary{}
	}
	writeJSON(w, http.StatusOK, results)
}

func (a *API) handleProjectGit(w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	proj, err := a.store.GetProject(projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "project_not_found", "Project not found")
		return
	}
	if proj.WorkDir == "" {
		writeError(w, http.StatusBadRequest, "no_work_dir", "Project has no work_dir configured")
		return
	}
	if _, err := os.Stat(proj.WorkDir); os.IsNotExist(err) {
		writeError(w, http.StatusBadRequest, "invalid_work_dir", "work_dir does not exist: "+proj.WorkDir)
		return
	}

	// Run git commands in the project directory
	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = proj.WorkDir
		out, _ := cmd.Output()
		return string(out)
	}

	// Get current branch
	branch := strings.TrimSpace(runGit("rev-parse", "--abbrev-ref", "HEAD"))

	// Get remote tracking branch
	remote := strings.TrimSpace(runGit("rev-parse", "--abbrev-ref", "@{upstream}"))

	// Fetch latest from origin (non-blocking, best-effort)
	exec.Command("git", "-C", proj.WorkDir, "fetch", "--quiet").Run()

	// Status
	status := runGit("status", "--short")

	// Diff: uncommitted changes
	diffLocal := runGit("diff")

	// Diff: staged changes
	diffStaged := runGit("diff", "--cached")

	// Diff: local commits not on remote
	var diffUpstream string
	var aheadBehind string
	if remote != "" {
		diffUpstream = runGit("diff", remote+"...HEAD")
		aheadBehind = strings.TrimSpace(runGit("rev-list", "--left-right", "--count", remote+"...HEAD"))
	}

	// Recent log
	logOutput := runGit("log", "--oneline", "-20")

	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":    projectID,
		"work_dir":      proj.WorkDir,
		"branch":        branch,
		"remote":        remote,
		"status":        status,
		"diff_local":    diffLocal,
		"diff_staged":   diffStaged,
		"diff_upstream": diffUpstream,
		"ahead_behind":  aheadBehind,
		"log":           logOutput,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// tryAutoDispatch checks if the agent's project has auto_dispatch enabled,
// and if so, assigns the highest-priority ready task with satisfied deps.
func (a *API) tryAutoDispatch(agentName, projectID string) {
	if projectID == "" {
		return
	}
	proj, err := a.store.GetProject(projectID)
	if err != nil || !proj.AutoDispatch {
		return
	}

	// Get ready tasks for the project, sorted by priority
	tasks, err := a.store.ListTasks(map[string]string{
		"status":     "ready",
		"project_id": projectID,
		"sort":       "priority",
	})
	if err != nil || len(tasks) == 0 {
		return
	}

	for _, task := range tasks {
		if task.Assignee != "" {
			continue
		}
		// Check deps are all satisfied (done)
		if len(task.DependsOn) > 0 {
			allDone := true
			for _, depID := range task.DependsOn {
				dep, err := a.store.GetTask(depID)
				if err != nil || dep.Status != model.TaskDone {
					allDone = false
					break
				}
			}
			if !allDone {
				continue
			}
		}

		// Claim the task for the agent
		if err := a.store.ClaimTask(task.ID, agentName); err != nil {
			continue
		}

		// Send message to agent
		msg := &model.Message{
			From: "waggle",
			To:   agentName,
			Body: fmt.Sprintf("Auto-assigned task: %s (%s). Priority: %s.", task.Title, task.ID, task.Priority),
		}
		a.store.SendMessage(msg)
		a.emit(&model.Event{
			Type:    model.EventTaskClaimed,
			AgentID: agentName,
			TaskID:  task.ID,
			Payload: map[string]string{"auto_dispatch": "true"},
		})
		log.Printf("Auto-dispatched task %s to agent %s", task.ID, agentName)
		return
	}
}

// wakeAgent ensures an agent is alive to handle a message.
// If the agent has a live session, the message is already stored and they'll pick it up via MCP.
// If not, auto-spawn the agent with a prompt to check their messages.
func (a *API) wakeAgent(agentName, message string) {
	// First check if agent is registered and alive in the store (covers MCP agents)
	if agent, err := a.store.GetAgentByName(agentName); err == nil {
		if agent.Status != model.AgentDisconnected && time.Since(agent.LastSeen) < 90*time.Second {
			// Agent is alive (registered via MCP or REST) — message is stored, they'll poll it
			return
		}
	}

	a.procsMu.Lock()
	_, alive := a.procs[agentName]
	a.procsMu.Unlock()

	if alive {
		// Agent is running as a spawned process
		return
	}

	// Agent not running — find their project and work_dir, then spawn
	agent, err := a.store.GetAgentByName(agentName)
	if err != nil {
		log.Printf("wakeAgent: unknown agent %s: %v", agentName, err)
		return
	}

	// Try to find work_dir: project.WorkDir first, then agentDirs cache, then conventions
	var workDir string
	if agent.ProjectID != "" {
		if proj, err := a.store.GetProject(agent.ProjectID); err == nil && proj.WorkDir != "" {
			workDir = proj.WorkDir
		}
	}
	if workDir == "" {
		a.procsMu.Lock()
		workDir = a.agentDirs[agentName]
		a.procsMu.Unlock()
	}
	if workDir == "" {
		home, _ := os.UserHomeDir()
		baseName := agentName
		for _, suffix := range []string{"-lead", "-dev", "-worker", "-reviewer", "-tester"} {
			baseName = strings.TrimSuffix(baseName, suffix)
		}
		for _, c := range []string{
			filepath.Join(home, "projects", baseName),
			filepath.Join(home, "projects", agentName),
		} {
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				workDir = c
				break
			}
		}
	}

	if workDir == "" {
		log.Printf("wakeAgent: no work_dir for agent %s, cannot auto-spawn", agentName)
		return
	}

	// Build spawn request — tell agent to check messages (the message is already stored in waggle)
	spawnBody, _ := json.Marshal(map[string]string{
		"name":       agentName,
		"project_id": agent.ProjectID,
		"work_dir":   workDir,
		"prompt":     "You have a new message from the user. Read your waggle messages and respond.",
	})
	spawnReq, _ := http.NewRequest(http.MethodPost, "/api/spawn", bytes.NewBuffer(spawnBody))
	spawnReq.Header.Set("Content-Type", "application/json")

	// Use a discard response writer — we don't need the spawn response here
	rec := &discardResponseWriter{code: 200}
	a.handleSpawn(rec, spawnReq)
	if rec.code >= 400 {
		log.Printf("wakeAgent: auto-spawn failed for %s (status %d)", agentName, rec.code)
	} else {
		log.Printf("wakeAgent: auto-spawned %s in %s", agentName, workDir)
	}
}

// discardResponseWriter captures status code but discards body
type discardResponseWriter struct {
	code   int
	header http.Header
}

func (d *discardResponseWriter) Header() http.Header {
	if d.header == nil {
		d.header = make(http.Header)
	}
	return d.header
}
func (d *discardResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardResponseWriter) WriteHeader(code int)         { d.code = code }

func agentLogPath(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".waggle", "logs", name+".log")
}

func (a *API) handleSpawn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		ProjectID    string `json:"project_id"`
		WorkDir      string `json:"work_dir"`
		Prompt       string `json:"prompt"`
		Model        string `json:"model"`
		PersonaID    string `json:"persona_id"`
		Mode         string `json:"mode"`
		SystemPrompt string `json:"system_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "missing_name", "agent name is required")
		return
	}
	if !safeNameRe.MatchString(req.Name) {
		writeError(w, http.StatusBadRequest, "invalid_name", "name must be alphanumeric with .-_ only, max 64 chars")
		return
	}

	if req.WorkDir == "" {
		writeError(w, http.StatusBadRequest, "missing_work_dir", "work_dir is required")
		return
	}

	// Resolve work dir
	workDir := req.WorkDir
	if !filepath.IsAbs(workDir) {
		home, _ := os.UserHomeDir()
		workDir = filepath.Join(home, workDir)
	}
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		writeError(w, http.StatusBadRequest, "invalid_work_dir", "directory does not exist: "+workDir)
		return
	}

	// Ensure .mcp.json has waggle config in the work dir
	mcpPath := filepath.Join(workDir, ".mcp.json")
	waggleBin, _ := os.Executable()
	if waggleBin == "" {
		waggleBin = "waggle"
	}
	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			"waggle": map[string]any{
				"command": waggleBin,
				"args":    []string{"mcp"},
			},
		},
	}
	// Merge with existing .mcp.json if present
	if data, err := os.ReadFile(mcpPath); err == nil {
		var existing map[string]any
		if json.Unmarshal(data, &existing) == nil {
			if servers, ok := existing["mcpServers"].(map[string]any); ok {
				servers["waggle"] = mcpConfig["mcpServers"].(map[string]any)["waggle"]
				mcpConfig = existing
			}
		}
	}
	mcpData, _ := json.MarshalIndent(mcpConfig, "", "  ")
	os.WriteFile(mcpPath, mcpData, 0644)

	// Find claude binary
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "claude_not_found", "claude CLI not found in PATH")
		return
	}

	// Build the initial prompt
	prompt := req.Prompt
	if prompt == "" {
		prompt = "You are agent '" + req.Name + "'. Register with waggle and check for tasks."
	}

	// Build registration preamble
	regPreamble := fmt.Sprintf(
		"First, register with waggle: use waggle_register_agent with name '%s'",
		req.Name,
	)
	if req.ProjectID != "" {
		regPreamble += fmt.Sprintf(" and project_id '%s'", req.ProjectID)
	}
	if req.PersonaID != "" {
		regPreamble += fmt.Sprintf(" and persona_id '%s'", req.PersonaID)
	}
	regPreamble += ". Then: " + prompt

	// Kill existing spawned process if any
	a.procsMu.Lock()
	if existing, ok := a.procs[req.Name]; ok {
		if pipe, ok := a.procsStdin[req.Name]; ok {
			pipe.Close()
		}
		existing.Process.Kill()
		existing.Wait()
		delete(a.procs, req.Name)
		delete(a.procsStdin, req.Name)
	}
	a.procsMu.Unlock()

	// Set up log file
	logPath := agentLogPath(req.Name)
	os.MkdirAll(filepath.Dir(logPath), 0755)
	logFile, err := os.Create(logPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "log_failed", "cannot create log file: "+err.Error())
		return
	}

	// Write prompt to a temp file to avoid shell escaping issues
	promptFile := filepath.Join(os.TempDir(), "waggle-prompt-"+req.Name+".txt")
	os.WriteFile(promptFile, []byte(regPreamble), 0644)

	// Build shell command using -p mode (single-turn, proven to work)
	shellCmd := fmt.Sprintf("%s --dangerously-skip-permissions", claudeBin)
	if req.Model != "" {
		shellCmd += " --model " + req.Model
	}
	shellCmd += fmt.Sprintf(` -p "$(cat '%s')"`, promptFile)

	// Write a launch script to avoid shell escaping issues entirely
	launchScript := filepath.Join(os.TempDir(), "waggle-launch-"+req.Name+".sh")
	os.WriteFile(launchScript, []byte("#!/bin/sh\nunset CLAUDECODE\n"+shellCmd), 0755)

	// Spawn as child process
	cmd := exec.Command("/bin/sh", launchScript)
	cmd.Dir = workDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Filter out CLAUDECODE env var to allow nested Claude Code sessions
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, e)
		}
	}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		writeError(w, http.StatusInternalServerError, "spawn_failed",
			fmt.Sprintf("failed to start agent process: %v", err))
		return
	}

	// Track the process and work dir
	a.procsMu.Lock()
	a.procs[req.Name] = cmd
	a.agentDirs[req.Name] = workDir
	a.procsMu.Unlock()

	// Clean up when process exits
	go func() {
		cmd.Wait()
		logFile.Close()
		a.procsMu.Lock()
		delete(a.procs, req.Name)
		delete(a.procsStdin, req.Name)
		a.procsMu.Unlock()
	}()

	// Record event
	a.emit(&model.Event{
		Type:    model.EventAgentJoined,
		AgentID: req.Name,
		Payload: map[string]string{
			"work_dir": workDir,
			"prompt":   prompt,
			"log":      logPath,
		},
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "spawned",
		"name":   req.Name,
		"log":    logPath,
	})
}

func (a *API) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	a.procsMu.Lock()
	seen := map[string]bool{}
	var sessions []map[string]any
	for name, cmd := range a.procs {
		seen[name] = true
		sessions = append(sessions, map[string]any{
			"name":    name,
			"agent":   name,
			"pid":     cmd.Process.Pid,
			"has_log": true,
			"alive":   true,
		})
	}
	a.procsMu.Unlock()

	// Include all agents with log files (even if process exited)
	if r.URL.Query().Get("all") == "true" {
		home, _ := os.UserHomeDir()
		logDir := filepath.Join(home, ".waggle", "logs")
		entries, _ := os.ReadDir(logDir)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".log")
			if seen[name] {
				continue
			}
			info, _ := e.Info()
			sessions = append(sessions, map[string]any{
				"name":      name,
				"agent":     name,
				"has_log":   true,
				"alive":     false,
				"log_size":  info.Size(),
				"log_mtime": info.ModTime().Format("2006-01-02T15:04:05Z"),
			})
		}
	}

	if sessions == nil {
		sessions = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (a *API) handleSessionAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	if name == "" {
		writeError(w, http.StatusBadRequest, "missing_name", "session name required")
		return
	}

	switch {
	case action == "output" && r.Method == http.MethodGet:
		// Read from log file
		logPath := agentLogPath(name)
		data, err := os.ReadFile(logPath)
		if err != nil {
			writeError(w, http.StatusNotFound, "no_log", "no log file for agent: "+name)
			return
		}
		lines := 100
		if l := r.URL.Query().Get("lines"); l != "" {
			fmt.Sscanf(l, "%d", &lines)
		}
		// Tail the output to requested number of lines
		allLines := strings.Split(string(data), "\n")
		start := 0
		if len(allLines) > lines {
			start = len(allLines) - lines
		}
		output := strings.Join(allLines[start:], "\n")
		writeJSON(w, http.StatusOK, map[string]string{"output": output, "agent": name})

	case action == "input" && r.Method == http.MethodPost:
		var req struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Input == "" {
			writeError(w, http.StatusBadRequest, "invalid_input", "input field required")
			return
		}
		a.procsMu.Lock()
		pipe, ok := a.procsStdin[name]
		a.procsMu.Unlock()
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "no spawned process for: "+name)
			return
		}
		// Write the input as a stream-json user message
		userMsg, _ := json.Marshal(map[string]string{"type": "user", "content": req.Input})
		if _, err := fmt.Fprintf(pipe, "%s\n", userMsg); err != nil {
			writeError(w, http.StatusInternalServerError, "write_failed", "failed to write to agent stdin: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "sent", "agent": name})

	case action == "" && r.Method == http.MethodDelete:
		a.procsMu.Lock()
		cmd, ok := a.procs[name]
		if ok {
			if pipe, pok := a.procsStdin[name]; pok {
				pipe.Close()
			}
			cmd.Process.Kill()
			delete(a.procs, name)
			delete(a.procsStdin, name)
		}
		a.procsMu.Unlock()

		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "no spawned process for: "+name)
			return
		}
		// Also disconnect the agent
		a.store.DisconnectAgent(name)
		a.emit(&model.Event{
			Type:    model.EventAgentLeft,
			AgentID: name,
		})
		writeJSON(w, http.StatusOK, map[string]string{"status": "killed", "agent": name})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "push notifications are not available")
}

func (a *API) handleTaskExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	tasks, err := a.store.ListTasks(map[string]string{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="waggle-tasks.json"`)
		json.NewEncoder(w).Encode(tasks)

	case "csv":
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="waggle-tasks.csv"`)
		// Write CSV header
		fmt.Fprintf(w, "id,title,status,priority,assignee,task_type,project_id,created_at,updated_at\n")
		for _, t := range tasks {
			title := strings.ReplaceAll(t.Title, `"`, `""`)
			desc := ""
			if t.Assignee != "" {
				desc = t.Assignee
			}
			fmt.Fprintf(w, `"%s","%s","%s","%s","%s","%s","%s","%s","%s"`+"\n",
				t.ID, title, t.Status, t.Priority, desc,
				t.TaskType, t.ProjectID,
				t.CreatedAt.Format("2006-01-02T15:04:05Z"),
				t.UpdatedAt.Format("2006-01-02T15:04:05Z"))
		}

	default:
		writeError(w, http.StatusBadRequest, "invalid_format", "format must be 'json' or 'csv'")
	}
}

func (a *API) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := a.store.GetAllSettings()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, settings)

	case http.MethodPut:
		var updates map[string]string
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		for key, value := range updates {
			if err := a.store.SetSetting(key, value); err != nil {
				writeError(w, http.StatusInternalServerError, "write_failed", err.Error())
				return
			}
		}
		settings, _ := a.store.GetAllSettings()
		writeJSON(w, http.StatusOK, settings)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	type alert struct {
		Level   string `json:"level"`   // warning, critical
		Type    string `json:"type"`    // stale_task, past_deadline, no_agents, stale_agent
		Title   string `json:"title"`
		Detail  string `json:"detail,omitempty"`
		TaskID  string `json:"task_id,omitempty"`
		AgentID string `json:"agent_id,omitempty"`
	}

	var alerts []alert
	now := time.Now().UTC()

	// Check for tasks stuck in_progress > 3 days
	if tasks, err := a.store.ListTasks(map[string]string{"status": "in_progress"}); err == nil {
		for _, t := range tasks {
			stuckDays := int(now.Sub(t.UpdatedAt).Hours() / 24)
			if stuckDays >= 3 {
				level := "warning"
				if stuckDays >= 7 {
					level = "critical"
				}
				alerts = append(alerts, alert{
					Level:  level,
					Type:   "stale_task",
					Title:  fmt.Sprintf("Task stuck for %d days", stuckDays),
					Detail: t.Title,
					TaskID: t.ID,
				})
			}
		}
	}

	// Check for tasks past deadline
	if tasks, err := a.store.ListTasks(map[string]string{}); err == nil {
		for _, t := range tasks {
			if t.Deadline != nil && t.Status != model.TaskDone && now.After(*t.Deadline) {
				overdueDays := int(now.Sub(*t.Deadline).Hours() / 24)
				level := "warning"
				if overdueDays >= 3 {
					level = "critical"
				}
				alerts = append(alerts, alert{
					Level:  level,
					Type:   "past_deadline",
					Title:  fmt.Sprintf("Overdue by %d days", overdueDays),
					Detail: t.Title,
					TaskID: t.ID,
				})
			}
		}
	}

	// Check for blocked tasks
	if tasks, err := a.store.ListTasks(map[string]string{"status": "blocked"}); err == nil {
		for _, t := range tasks {
			blockedDays := int(now.Sub(t.UpdatedAt).Hours() / 24)
			if blockedDays >= 1 {
				alerts = append(alerts, alert{
					Level:  "warning",
					Type:   "blocked_task",
					Title:  fmt.Sprintf("Blocked for %d days", blockedDays),
					Detail: t.Title,
					TaskID: t.ID,
				})
			}
		}
	}

	// Check agent health
	if agents, err := a.store.ListAgents(""); err == nil {
		activeCount := 0
		for _, ag := range agents {
			if ag.Status == model.AgentDisconnected {
				continue
			}
			activeCount++
			sinceSeen := now.Sub(ag.LastSeen)
			if sinceSeen > 60*time.Second {
				alerts = append(alerts, alert{
					Level:   "warning",
					Type:    "stale_agent",
					Title:   "Agent heartbeat late",
					Detail:  fmt.Sprintf("%s last seen %s ago", ag.Name, sinceSeen.Round(time.Second)),
					AgentID: ag.Name,
				})
			}
		}
		if activeCount == 0 {
			alerts = append(alerts, alert{
				Level: "critical",
				Type:  "no_agents",
				Title: "No active agents",
			})
		}
	}

	if alerts == nil {
		alerts = []alert{}
	}
	writeJSON(w, http.StatusOK, alerts)
}

func limitBody(r *http.Request) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodySize)
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

// --- Context Manager Handlers ---

func (a *API) handleProgress(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projectID := r.URL.Query().Get("project_id")
		limitStr := r.URL.Query().Get("limit")
		limit := 20
		if limitStr != "" {
			fmt.Sscanf(limitStr, "%d", &limit)
		}
		if projectID != "" {
			entries, err := a.store.ListProgress(projectID, limit)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, entries)
		} else {
			entries, err := a.store.ListAllProgress(limit)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, entries)
		}

	case http.MethodPost:
		var p model.Progress
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if p.ProjectID == "" || p.Source == "" || p.Summary == "" {
			writeError(w, http.StatusBadRequest, "missing_fields", "project_id, source, and summary are required")
			return
		}
		if err := a.store.CreateProgress(&p); err != nil {
			writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
			return
		}
		a.emit(&model.Event{Type: "progress_logged", TaskID: p.ProjectID, Payload: p})
		writeJSON(w, http.StatusCreated, p)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleCtxSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projectID := r.URL.Query().Get("project_id")
		if projectID == "" {
			writeError(w, http.StatusBadRequest, "missing_project", "project_id is required")
			return
		}
		sessions, err := a.store.ListSessions(projectID, 20)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sessions)

	case http.MethodPost:
		var sess model.Session
		if err := json.NewDecoder(r.Body).Decode(&sess); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if sess.ProjectID == "" {
			writeError(w, http.StatusBadRequest, "missing_project", "project_id is required")
			return
		}
		if err := a.store.CreateSession(&sess); err != nil {
			writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, sess)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleCtxSessionAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/ctx-sessions/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "session id required")
		return
	}
	sessID := parts[0]

	if r.Method == http.MethodPost && len(parts) >= 2 && parts[1] == "end" {
		var body struct {
			Summary string `json:"summary"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if err := a.store.EndSession(sessID, body.Summary); err != nil {
			writeError(w, http.StatusInternalServerError, "end_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ended"})
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (a *API) handleBriefing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "missing_project", "project_id is required")
		return
	}

	project, err := a.store.GetProject(projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}

	timeSince := "never"
	if project.LastTouchedAt != nil {
		dur := time.Since(*project.LastTouchedAt)
		switch {
		case dur < time.Hour:
			timeSince = fmt.Sprintf("%d minutes ago", int(dur.Minutes()))
		case dur < 24*time.Hour:
			timeSince = fmt.Sprintf("%d hours ago", int(dur.Hours()))
		default:
			timeSince = fmt.Sprintf("%d days ago", int(dur.Hours()/24))
		}
	}

	taskCount, _ := a.store.TaskCountByProject(projectID)
	progress, _ := a.store.ListProgress(projectID, 5)
	sessions, _ := a.store.ListSessions(projectID, 3)

	openTasks := []map[string]string{}
	if tasks, err := a.store.ListTasks(map[string]string{"project_id": projectID, "sort": "priority"}); err == nil {
		for _, t := range tasks {
			if t.Status == model.TaskDone {
				continue
			}
			if len(openTasks) >= 3 {
				break
			}
			openTasks = append(openTasks, map[string]string{
				"id":       t.ID,
				"title":    t.Title,
				"priority": string(t.Priority),
				"status":   string(t.Status),
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project":         project,
		"last_touched":    timeSince,
		"open_task_count": taskCount,
		"top_tasks":       openTasks,
		"recent_progress": progress,
		"recent_sessions": sessions,
	})
}

func (a *API) handleWhatsNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	accountFilter := r.URL.Query().Get("account")
	projects, err := a.store.ListProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}

	type projectSummary struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Status        string `json:"status"`
		Health        string `json:"health"`
		Account       string `json:"account"`
		Category      string `json:"category"`
		ParkingNote   string `json:"parking_note,omitempty"`
		RevenueStatus string `json:"revenue_status,omitempty"`
		LastTouched   string `json:"last_touched"`
		DaysSince     int    `json:"days_since_touched"`
		OpenTasks     int    `json:"open_tasks"`
		Urgency       string `json:"urgency"`
		Reason        string `json:"reason,omitempty"`
	}

	var items []projectSummary
	urgencyOrder := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}

	for _, p := range projects {
		if accountFilter != "" && p.Account != accountFilter {
			continue
		}
		if p.Status == model.ProjectKilled {
			continue
		}

		taskCount, _ := a.store.TaskCountByProject(p.ID)

		daysSince := 0
		lastTouched := "never"
		if p.LastTouchedAt != nil {
			daysSince = int(time.Since(*p.LastTouchedAt).Hours() / 24)
			switch {
			case daysSince == 0:
				lastTouched = "today"
			case daysSince == 1:
				lastTouched = "yesterday"
			default:
				lastTouched = fmt.Sprintf("%d days ago", daysSince)
			}
		}

		urgency := "low"
		reason := ""
		switch {
		case p.Health == model.HealthRed:
			urgency = "critical"
			reason = "project health is red"
		case p.RevenueStatus == "stalled":
			urgency = "high"
			reason = "revenue stalled"
		case taskCount > 0 && daysSince > 14:
			urgency = "medium"
			reason = fmt.Sprintf("%d open tasks, untouched %d days", taskCount, daysSince)
		case p.Health == model.HealthYellow:
			urgency = "medium"
			reason = "project health is yellow"
		}

		if p.Status == model.ProjectDormant && urgency == "low" {
			continue
		}

		items = append(items, projectSummary{
			ID:            p.ID,
			Name:          p.Name,
			Status:        string(p.Status),
			Health:        string(p.Health),
			Account:       p.Account,
			Category:      p.Category,
			ParkingNote:   p.ParkingNote,
			RevenueStatus: p.RevenueStatus,
			LastTouched:   lastTouched,
			DaysSince:     daysSince,
			OpenTasks:     taskCount,
			Urgency:       urgency,
			Reason:        reason,
		})
	}

	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if urgencyOrder[items[j].Urgency] < urgencyOrder[items[i].Urgency] {
				items[i], items[j] = items[j], items[i]
			}
		}
	}

	writeJSON(w, http.StatusOK, items)
}

func (a *API) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Can target a single project or check all with work_dir
	projectID := r.URL.Query().Get("project_id")

	var projects []*model.Project
	if projectID != "" {
		p, err := a.store.GetProject(projectID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "project not found")
			return
		}
		projects = []*model.Project{p}
	} else {
		var err error
		projects, err = a.store.ListProjects()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
	}

	type checkResult struct {
		ProjectID string `json:"project_id"`
		Name      string `json:"name"`
		Health    string `json:"health"`
		Reason    string `json:"reason"`
	}

	var results []checkResult

	for _, p := range projects {
		if p.WorkDir == "" || p.Status == model.ProjectKilled || p.Status == model.ProjectDormant {
			continue
		}

		// Expand ~ in work_dir
		workDir := p.WorkDir
		if strings.HasPrefix(workDir, "~/") {
			home, _ := os.UserHomeDir()
			workDir = filepath.Join(home, workDir[2:])
		}

		// Check if directory exists
		if _, err := os.Stat(workDir); err != nil {
			continue
		}

		health := "green"
		reason := ""

		// Check git status — are there uncommitted changes?
		gitCmd := exec.CommandContext(r.Context(), "git", "-C", workDir, "status", "--porcelain")
		gitOut, err := gitCmd.Output()
		if err == nil && len(gitOut) > 0 {
			lines := strings.Count(string(gitOut), "\n")
			if lines > 10 {
				health = "yellow"
				reason = fmt.Sprintf("%d uncommitted changes", lines)
			}
		}

		// Check for test failures — look for common test runners
		var testCmd *exec.Cmd
		switch p.TechStack {
		case "clojure":
			testCmd = exec.CommandContext(r.Context(), "clj", "-M:test", "--reporter", "silent")
		case "go":
			testCmd = exec.CommandContext(r.Context(), "go", "test", "./...")
		case "typescript", "javascript":
			testCmd = exec.CommandContext(r.Context(), "npm", "test", "--", "--silent")
		case "python":
			testCmd = exec.CommandContext(r.Context(), "python", "-m", "pytest", "--tb=no", "-q")
		}

		if testCmd != nil {
			testCmd.Dir = workDir
			if err := testCmd.Run(); err != nil {
				health = "red"
				reason = "tests failing"
			}
		}

		// Check last commit age
		lastCommitCmd := exec.CommandContext(r.Context(), "git", "-C", workDir, "log", "-1", "--format=%ct")
		if lastOut, err := lastCommitCmd.Output(); err == nil {
			var ts int64
			if _, err := fmt.Sscanf(strings.TrimSpace(string(lastOut)), "%d", &ts); err == nil {
				age := time.Since(time.Unix(ts, 0))
				if age > 30*24*time.Hour && health == "green" {
					if p.Status != model.ProjectDormant {
						health = "yellow"
						reason = fmt.Sprintf("no commits in %d days", int(age.Hours()/24))
					}
				}
				// Update last_touched_at
				lastCommit := time.Unix(ts, 0)
				if p.LastTouchedAt == nil || lastCommit.After(*p.LastTouchedAt) {
					a.store.UpdateProject(p.ID, map[string]any{
						"last_touched_at": lastCommit.Format(time.RFC3339),
					})
				}
			}
		}

		// Update project health
		a.store.UpdateProject(p.ID, map[string]any{"health": health})

		results = append(results, checkResult{
			ProjectID: p.ID,
			Name:      p.Name,
			Health:    health,
			Reason:    reason,
		})
	}

	writeJSON(w, http.StatusOK, results)
}
