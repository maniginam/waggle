package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	protocolVersion = "2025-03-26"
	serverName      = "waggle"
	serverVersion   = "0.1.0"
)

type Adapter struct {
	baseURL   string
	agentName string
	in        io.Reader
	out       io.Writer
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
				"name": prop("string", "Agent name (e.g. 'backend-agent')"),
				"type": prop("string", "Agent type (e.g. 'claude-code', 'cursor', 'aider')"),
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
				"q":        prop("string", "Search title and description"),
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
		// Register via REST — create agent by posting a task claim will trigger it,
		// but we need a direct register endpoint. Use internal knowledge that
		// RegisterAgent is called on WS connect. For REST, we'll create a simple registration.
		body, _ := json.Marshal(map[string]string{"name": agentName, "type": agentType})
		resp, err := http.Post(a.baseURL+"/api/agents/register", "application/json", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("server not reachable: %w", err)
		}
		defer resp.Body.Close()
		var result any
		json.NewDecoder(resp.Body).Decode(&result)
		return result, nil

	case "waggle_create_task":
		return a.postJSON("/api/tasks", args)

	case "waggle_list_tasks":
		params := []string{}
		for _, key := range []string{"status", "assignee", "tag", "priority", "q"} {
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
