package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	protocolVersion = "2025-03-26"
	serverName      = "waggle"
	serverVersion   = "0.1.0"
)

type Adapter struct {
	baseURL       string
	agentName     string
	in            io.Reader
	out           io.Writer
	stopHeartbeat chan struct{}
	heartbeatOnce sync.Once
}

func NewAdapter(baseURL string) *Adapter {
	return &Adapter{
		baseURL: baseURL,
		in:      os.Stdin,
		out:     os.Stdout,
	}
}

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *jsonrpcError `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (a *Adapter) Run() error {
	scanner := bufio.NewScanner(a.in)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			a.sendError(nil, -32700, "Parse error", err.Error())
			continue
		}

		a.handleRequest(&req)
	}
	return scanner.Err()
}

func (a *Adapter) handleRequest(req *jsonrpcRequest) {
	switch req.Method {
	case "initialize":
		a.handleInitialize(req)
	case "initialized":
		// Notification, no response needed
	case "ping":
		a.sendResult(req.ID, map[string]any{})
	case "tools/list":
		a.handleToolsList(req)
	case "tools/call":
		a.handleToolsCall(req)
	default:
		a.sendError(req.ID, -32601, "Method not found", req.Method)
	}
}

func (a *Adapter) handleInitialize(req *jsonrpcRequest) {
	a.sendResult(req.ID, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
	})
}

func (a *Adapter) handleToolsList(req *jsonrpcRequest) {
	tools := []map[string]any{
		toolDef("waggle_register_agent", "Register this agent with the Waggle server. Must be called first.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":         prop("string", "Agent name (e.g. 'backend-agent')"),
				"type":         prop("string", "Agent type (e.g. 'claude-code', 'cursor', 'aider')"),
				"project_id":   prop("string", "Project ID to assign this agent to (optional)"),
				"role":         prop("string", "Agent role: 'alpha', 'leader', or 'worker' (default: 'worker')"),
				"parent_agent": prop("string", "Name of the parent agent (leader or alpha) that manages this agent"),
			},
			"required": []string{"name", "type"},
		}),
		toolDef("waggle_create_task", "Create a new SMART task.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":       prop("string", "Task title"),
				"description": prop("string", "Detailed description"),
				"criteria":    propArray("string", "Acceptance criteria (the 'Measurable' in SMART)"),
				"priority":    propEnum("string", []string{"critical", "high", "medium", "low"}, "Task priority"),
				"tags":        propArray("string", "Tags for categorization (the 'Relevant' in SMART)"),
				"estimate":    prop("string", "Time estimate (e.g. '2h', '1d')"),
				"deadline":    prop("string", "Deadline in RFC3339 format"),
				"parent_id":   prop("string", "Parent task ID for subtasks"),
				"depends_on":  propArray("string", "IDs of tasks this depends on"),
				"task_type":   propEnum("string", []string{"task", "epic", "story", "issue"}, "Task type (default: task)"),
				"project_id":  prop("string", "Project ID this task belongs to"),
			},
			"required": []string{"title"},
		}),
		toolDef("waggle_list_tasks", "List tasks with optional filters.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status":   propEnum("string", []string{"backlog", "ready", "in_progress", "review", "done", "blocked"}, "Filter by status"),
				"assignee": prop("string", "Filter by assignee name"),
				"tag":      prop("string", "Filter by tag"),
				"priority": propEnum("string", []string{"critical", "high", "medium", "low"}, "Filter by priority"),
				"q":          prop("string", "Search title and description"),
				"sort":       propEnum("string", []string{"priority", "deadline", "updated", "title", "status"}, "Sort field"),
				"order":      propEnum("string", []string{"asc", "desc"}, "Sort direction"),
				"task_type":  propEnum("string", []string{"task", "epic", "story", "issue"}, "Filter by task type"),
				"project_id": prop("string", "Filter by project ID"),
			},
		}),
		toolDef("waggle_show_task", "Show details of a specific task.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Task ID")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_update_task", "Update an existing task.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          prop("string", "Task ID"),
				"status":      propEnum("string", []string{"backlog", "ready", "in_progress", "review", "done", "blocked"}, "New status"),
				"priority":    propEnum("string", []string{"critical", "high", "medium", "low"}, "New priority"),
				"assignee":    prop("string", "New assignee"),
				"title":       prop("string", "New title"),
				"description": prop("string", "New description"),
				"criteria":    propArray("string", "New acceptance criteria"),
			},
			"required": []string{"id"},
		}),
		toolDef("waggle_claim_task", "Claim a task for the registered agent.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Task ID to claim")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_unclaim_task", "Release a claimed task (self-only).", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Task ID to unclaim")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_complete_task", "Mark a task as done.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Task ID to complete")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_delete_task", "Delete a task (fails if in_progress).", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Task ID to delete")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_get_next_task", "Get the highest-priority ready task to work on. Returns the best available task.", map[string]any{
			"type":       "object",
			"properties": map[string]any{
				"tag": prop("string", "Optional tag filter"),
			},
		}),
		toolDef("waggle_task_deps", "Show task dependencies: what it depends on and what it blocks.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Task ID")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_task_history", "View the event log for a specific task.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Task ID")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_list_subtasks", "List subtasks and progress for a parent task.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Parent task ID")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_add_comment", "Add a comment/note to a task.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":   prop("string", "Task ID"),
				"body": prop("string", "Comment text"),
			},
			"required": []string{"id", "body"},
		}),
		toolDef("waggle_list_comments", "List comments on a task.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Task ID")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_briefing", "Get a complete status briefing: your current task, unread messages, available tasks, and team status. Call this when starting work or after a break.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		toolDef("waggle_list_agents", "List connected agents.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": propEnum("string", []string{"connected", "working", "idle", "blocked", "disconnected"}, "Filter by status"),
			},
		}),
		toolDef("waggle_set_status", "Set the calling agent's status.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status":       propEnum("string", []string{"connected", "working", "idle", "blocked"}, "Agent status"),
				"current_task": prop("string", "Current task ID"),
			},
			"required": []string{"status"},
		}),
		toolDef("waggle_send_message", "Send a message to another agent or broadcast.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to":   prop("string", "Recipient agent name (empty for broadcast)"),
				"body": prop("string", "Message body"),
			},
			"required": []string{"body"},
		}),
		toolDef("waggle_read_messages", "Read messages for the registered agent.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "Max messages to return"},
				"from":  prop("string", "Filter by sender"),
			},
		}),
		toolDef("waggle_search_messages", "Search message history by keyword.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": prop("string", "Search term to find in message bodies"),
				"limit": map[string]any{"type": "integer", "description": "Max results (default 50)"},
			},
			"required": []string{"query"},
		}),
		toolDef("waggle_submit_review", "Submit a git diff for review on a task. The diff will appear in the dashboard for the user to approve or reject.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": prop("string", "Task ID this diff is for"),
				"diff":    prop("string", "Git diff output"),
				"branch":  prop("string", "Git branch name"),
				"summary": prop("string", "Brief summary of changes"),
			},
			"required": []string{"task_id", "diff"},
		}),
		toolDef("waggle_list_reviews", "List code reviews. Shows pending reviews by default.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": propEnum("string", []string{"pending", "approved", "rejected"}, "Filter by review status"),
			},
		}),
		toolDef("waggle_create_project", "Create a new project to organize epics and stories.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        prop("string", "Project name"),
				"description": prop("string", "Project description"),
			},
			"required": []string{"name"},
		}),
		toolDef("waggle_list_projects", "List all projects.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		toolDef("waggle_show_project", "Show project details including its epics.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Project ID")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_update_project", "Update a project.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          prop("string", "Project ID"),
				"name":        prop("string", "New name"),
				"description": prop("string", "New description"),
			},
			"required": []string{"id"},
		}),
		toolDef("waggle_delete_project", "Delete a project.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Project ID")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_poke_agent", "Send a poke/check-in request to a stale or unresponsive agent. Use when an agent hasn't responded in 3+ minutes.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent": prop("string", "Name of the agent to poke"),
				"message": prop("string", "Optional message to include with the poke"),
			},
			"required": []string{"agent"},
		}),
		toolDef("waggle_heartbeat", "Send a manual heartbeat. Note: heartbeats are now sent automatically every 45s after registration. Only call this if you need an immediate heartbeat.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		toolDef("waggle_disconnect", "Gracefully disconnect this agent. Call before shutting down to mark yourself as disconnected and release claimed tasks.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		toolDef("waggle_report_usage", "Report token usage for this agent. Call periodically to track costs.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model":         prop("string", "Model name (e.g. 'claude-opus-4-6', 'claude-sonnet-4-6')"),
				"input_tokens":  map[string]any{"type": "integer", "description": "Number of input tokens consumed"},
				"output_tokens": map[string]any{"type": "integer", "description": "Number of output tokens consumed"},
				"task_id":       prop("string", "Task ID this usage is associated with"),
			},
			"required": []string{"input_tokens", "output_tokens"},
		}),
		toolDef("waggle_get_usage", "Get token usage summary. Shows total cost and per-agent breakdown.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
	}

	a.sendResult(req.ID, map[string]any{"tools": tools})
}

func (a *Adapter) handleToolsCall(req *jsonrpcRequest) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &call); err != nil {
		a.sendError(req.ID, -32602, "Invalid params", err.Error())
		return
	}

	var args map[string]any
	if len(call.Arguments) > 0 {
		json.Unmarshal(call.Arguments, &args)
	}
	if args == nil {
		args = map[string]any{}
	}

	result, err := a.executeTool(call.Name, args)
	if err != nil {
		a.sendResult(req.ID, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": fmt.Sprintf("Error: %s", err.Error())},
			},
			"isError": true,
		})
		return
	}

	text, _ := json.MarshalIndent(result, "", "  ")
	a.sendResult(req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(text)},
		},
	})
}

func (a *Adapter) executeTool(name string, args map[string]any) (any, error) {
	switch name {
	case "waggle_register_agent":
		agentName, _ := args["name"].(string)
		agentType, _ := args["type"].(string)
		if agentName == "" || agentType == "" {
			return nil, fmt.Errorf("name and type are required")
		}
		a.agentName = agentName
		projectID, _ := args["project_id"].(string)
		role, _ := args["role"].(string)
		parentAgent, _ := args["parent_agent"].(string)
		regPayload := map[string]string{"name": agentName, "type": agentType}
		if projectID != "" {
			regPayload["project_id"] = projectID
		}
		if role != "" {
			regPayload["role"] = role
		}
		if parentAgent != "" {
			regPayload["parent_agent"] = parentAgent
		}
		body, _ := json.Marshal(regPayload)
		resp, err := http.Post(a.baseURL+"/api/agents/register", "application/json", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("server not reachable: %w", err)
		}
		defer resp.Body.Close()
		var result any
		json.NewDecoder(resp.Body).Decode(&result)
		// Start auto-heartbeat in background
		a.startHeartbeat()
		return result, nil

	case "waggle_create_task":
		return a.postJSON("/api/tasks", args)

	case "waggle_list_tasks":
		params := []string{}
		for _, key := range []string{"status", "assignee", "tag", "priority", "q", "sort", "order", "task_type", "project_id"} {
			if v, ok := args[key].(string); ok && v != "" {
				params = append(params, key+"="+v)
			}
		}
		url := "/api/tasks"
		if len(params) > 0 {
			url += "?" + strings.Join(params, "&")
		}
		return a.get(url)

	case "waggle_show_task":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		return a.get("/api/tasks/" + id)

	case "waggle_update_task":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		updates := map[string]any{}
		for k, v := range args {
			if k != "id" {
				updates[k] = v
			}
		}
		return a.patchJSON("/api/tasks/"+id, updates)

	case "waggle_claim_task":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		if a.agentName == "" {
			return nil, fmt.Errorf("must call waggle_register_agent first")
		}
		return a.postJSON("/api/tasks/"+id+"/claim", map[string]string{"agent": a.agentName})

	case "waggle_unclaim_task":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		if a.agentName == "" {
			return nil, fmt.Errorf("must call waggle_register_agent first")
		}
		return a.postJSON("/api/tasks/"+id+"/unclaim", map[string]string{"agent": a.agentName})

	case "waggle_complete_task":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		return a.postJSON("/api/tasks/"+id+"/complete", nil)

	case "waggle_task_deps":
		taskID, _ := args["id"].(string)
		if taskID == "" {
			return nil, fmt.Errorf("id is required")
		}
		return a.get("/api/tasks/" + taskID + "/deps")

	case "waggle_task_history":
		taskID, _ := args["id"].(string)
		if taskID == "" {
			return nil, fmt.Errorf("id is required")
		}
		return a.get("/api/tasks/" + taskID + "/history")

	case "waggle_list_subtasks":
		taskID, _ := args["id"].(string)
		if taskID == "" {
			return nil, fmt.Errorf("id is required")
		}
		return a.get("/api/tasks/" + taskID + "/subtasks")

	case "waggle_add_comment":
		taskID, _ := args["id"].(string)
		body, _ := args["body"].(string)
		if taskID == "" || body == "" {
			return nil, fmt.Errorf("id and body are required")
		}
		author := a.agentName
		if author == "" {
			author = "anonymous"
		}
		return a.postJSON("/api/tasks/"+taskID+"/comments", map[string]string{
			"author": author,
			"body":   body,
		})

	case "waggle_list_comments":
		taskID, _ := args["id"].(string)
		if taskID == "" {
			return nil, fmt.Errorf("id is required")
		}
		return a.get("/api/tasks/" + taskID + "/comments")

	case "waggle_delete_task":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		return a.deleteJSON("/api/tasks/" + id)

	case "waggle_get_next_task":
		params := []string{"status=ready"}
		if tag, ok := args["tag"].(string); ok && tag != "" {
			params = append(params, "tag="+tag)
		}
		url := "/api/tasks?" + strings.Join(params, "&")
		result, err := a.get(url)
		if err != nil {
			return nil, err
		}
		tasks, ok := result.([]any)
		if !ok || len(tasks) == 0 {
			return map[string]any{"message": "No ready tasks available"}, nil
		}
		// Find highest priority task
		priorityOrder := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
		best := tasks[0].(map[string]any)
		bestPri := 4
		if p, ok := best["priority"].(string); ok {
			if v, ok := priorityOrder[p]; ok {
				bestPri = v
			}
		}
		for _, t := range tasks[1:] {
			task := t.(map[string]any)
			pri := 4
			if p, ok := task["priority"].(string); ok {
				if v, ok := priorityOrder[p]; ok {
					pri = v
				}
			}
			if pri < bestPri {
				best = task
				bestPri = pri
			}
		}
		return best, nil

	case "waggle_briefing":
		briefing := map[string]any{}

		// Agent info
		if a.agentName != "" {
			encodedName := url.PathEscape(a.agentName)
			queryName := url.QueryEscape(a.agentName)

			if agent, err := a.get("/api/agents/" + encodedName); err == nil {
				briefing["you"] = agent
			}

			// Unread messages
			if msgs, err := a.get("/api/messages?to=" + queryName + "&limit=10"); err == nil {
				briefing["messages"] = msgs
			}

			// Your assigned tasks
			if myTasks, err := a.get("/api/tasks?assignee=" + queryName); err == nil {
				briefing["your_tasks"] = myTasks
			}
		}

		// Stats
		if stats, err := a.get("/api/stats"); err == nil {
			briefing["stats"] = stats
		}

		// Ready tasks (sorted by priority)
		if tasks, err := a.get("/api/tasks?status=ready&sort=priority"); err == nil {
			briefing["ready_tasks"] = tasks
		}

		// Active agents
		if agents, err := a.get("/api/agents"); err == nil {
			briefing["team"] = agents
		}

		// Token usage summary
		if usage, err := a.get("/api/usage"); err == nil {
			briefing["usage"] = usage
		}

		return briefing, nil

	case "waggle_list_agents":
		url := "/api/agents"
		if status, ok := args["status"].(string); ok && status != "" {
			url += "?status=" + status
		}
		return a.get(url)

	case "waggle_set_status":
		if a.agentName == "" {
			return nil, fmt.Errorf("must call waggle_register_agent first")
		}
		status, _ := args["status"].(string)
		currentTask, _ := args["current_task"].(string)
		return a.postJSON("/api/agents/"+a.agentName+"/status", map[string]string{
			"status":       status,
			"current_task": currentTask,
		})

	case "waggle_send_message":
		if a.agentName == "" {
			return nil, fmt.Errorf("must call waggle_register_agent first")
		}
		to, _ := args["to"].(string)
		body, _ := args["body"].(string)
		if body == "" {
			return nil, fmt.Errorf("body is required")
		}
		return a.postJSON("/api/messages", map[string]string{
			"from": a.agentName,
			"to":   to,
			"body": body,
		})

	case "waggle_read_messages":
		if a.agentName == "" {
			return nil, fmt.Errorf("must call waggle_register_agent first")
		}
		url := "/api/messages?to=" + a.agentName
		if limit, ok := args["limit"].(float64); ok {
			url += fmt.Sprintf("&limit=%d", int(limit))
		}
		return a.get(url)

	case "waggle_search_messages":
		q, _ := args["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("query is required")
		}
		url := "/api/messages?q=" + url.QueryEscape(q)
		if limit, ok := args["limit"].(float64); ok {
			url += fmt.Sprintf("&limit=%d", int(limit))
		}
		return a.get(url)

	case "waggle_submit_review":
		if a.agentName == "" {
			return nil, fmt.Errorf("must call waggle_register_agent first")
		}
		taskID, _ := args["task_id"].(string)
		diff, _ := args["diff"].(string)
		if taskID == "" || diff == "" {
			return nil, fmt.Errorf("task_id and diff are required")
		}
		branch, _ := args["branch"].(string)
		summary, _ := args["summary"].(string)
		return a.postJSON("/api/reviews", map[string]string{
			"task_id":  taskID,
			"agent_id": a.agentName,
			"diff":     diff,
			"branch":   branch,
			"summary":  summary,
		})

	case "waggle_list_reviews":
		url := "/api/reviews"
		if status, ok := args["status"].(string); ok && status != "" {
			url += "?status=" + status
		}
		return a.get(url)

	case "waggle_create_project":
		return a.postJSON("/api/projects", args)

	case "waggle_list_projects":
		return a.get("/api/projects")

	case "waggle_show_project":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		// Return project + its epics
		project, err := a.get("/api/projects/" + id)
		if err != nil {
			return nil, err
		}
		epics, _ := a.get("/api/projects/" + id + "/epics")
		return map[string]any{
			"project": project,
			"epics":   epics,
		}, nil

	case "waggle_update_project":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		updates := map[string]any{}
		for k, v := range args {
			if k != "id" {
				updates[k] = v
			}
		}
		return a.patchJSON("/api/projects/"+id, updates)

	case "waggle_delete_project":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		return a.deleteJSON("/api/projects/" + id)

	case "waggle_poke_agent":
		if a.agentName == "" {
			return nil, fmt.Errorf("must call waggle_register_agent first")
		}
		agent, _ := args["agent"].(string)
		if agent == "" {
			return nil, fmt.Errorf("agent name is required")
		}
		msg, _ := args["message"].(string)
		if msg == "" {
			msg = "[POKE] You appear to be stalled or inactive. Please check in immediately: report your current status, what you are working on, and whether you are blocked. If blocked, describe what is blocking you."
		}
		return a.postJSON("/api/messages", map[string]string{
			"from": a.agentName,
			"to":   agent,
			"body": msg,
		})

	case "waggle_heartbeat":
		if a.agentName == "" {
			return nil, fmt.Errorf("must call waggle_register_agent first")
		}
		return a.postJSON("/api/agents/"+url.PathEscape(a.agentName)+"/status", map[string]string{
			"status": "connected",
		})

	case "waggle_disconnect":
		if a.agentName == "" {
			return nil, fmt.Errorf("must call waggle_register_agent first")
		}
		a.StopHeartbeat()
		return a.postJSON("/api/agents/"+url.PathEscape(a.agentName)+"/status", map[string]string{
			"status": "disconnected",
		})

	case "waggle_report_usage":
		agentName := a.agentName
		if agentName == "" {
			agentName = "unknown"
		}
		payload := map[string]any{
			"agent_name": agentName,
		}
		if m, ok := args["model"].(string); ok {
			payload["model"] = m
		}
		if v, ok := args["input_tokens"].(float64); ok {
			payload["input_tokens"] = int64(v)
		}
		if v, ok := args["output_tokens"].(float64); ok {
			payload["output_tokens"] = int64(v)
		}
		if v, ok := args["task_id"].(string); ok {
			payload["task_id"] = v
		}
		return a.postJSON("/api/usage", payload)

	case "waggle_get_usage":
		return a.get("/api/usage")

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func (a *Adapter) get(path string) (any, error) {
	resp, err := http.Get(a.baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result any
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%v", result)
	}
	return result, nil
}

func (a *Adapter) postJSON(path string, body any) (any, error) {
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	resp, err := http.Post(a.baseURL+path, "application/json", reader)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result any
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%v", result)
	}
	return result, nil
}

func (a *Adapter) patchJSON(path string, body any) (any, error) {
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPatch, a.baseURL+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result any
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%v", result)
	}
	return result, nil
}

func (a *Adapter) deleteJSON(path string) (any, error) {
	req, _ := http.NewRequest(http.MethodDelete, a.baseURL+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result any
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%v", result)
	}
	if result == nil {
		return map[string]any{"status": "deleted"}, nil
	}
	return result, nil
}

func (a *Adapter) sendResult(id any, result any) {
	resp := jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: result}
	data, _ := json.Marshal(resp)
	fmt.Fprintf(a.out, "%s\n", data)
}

func (a *Adapter) sendError(id any, code int, message, data string) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: message, Data: data},
	}
	d, _ := json.Marshal(resp)
	fmt.Fprintf(a.out, "%s\n", d)
}

// Schema helpers

func toolDef(name, description string, inputSchema map[string]any) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": inputSchema,
	}
}

func prop(typeName, description string) map[string]any {
	return map[string]any{"type": typeName, "description": description}
}

func propEnum(typeName string, values []string, description string) map[string]any {
	return map[string]any{"type": typeName, "enum": values, "description": description}
}

func propArray(itemType, description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": itemType},
		"description": description,
	}
}

func (a *Adapter) startHeartbeat() {
	a.heartbeatOnce.Do(func() {
		a.stopHeartbeat = make(chan struct{})
		go func() {
			ticker := time.NewTicker(45 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-a.stopHeartbeat:
					return
				case <-ticker.C:
					if a.agentName == "" {
						return
					}
					body, _ := json.Marshal(map[string]string{"status": "connected"})
					resp, err := http.Post(
						a.baseURL+"/api/agents/"+url.PathEscape(a.agentName)+"/status",
						"application/json",
						bytes.NewReader(body),
					)
					if err != nil {
						log.Printf("heartbeat failed: %v", err)
						continue
					}
					resp.Body.Close()
				}
			}
		}()
	})
}

func (a *Adapter) StopHeartbeat() {
	if a.stopHeartbeat != nil {
		close(a.stopHeartbeat)
	}
}
