package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
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
	outMu     sync.Mutex
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
		// --- Context Manager tools (primary interface) ---
		toolDef("waggle_ctx_briefing", "Get a complete briefing for a project: where you left off, open tasks, recent progress, session history. Call this when starting work on a project.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": prop("string", "Project ID to get briefing for"),
			},
			"required": []string{"project_id"},
		}),
		toolDef("waggle_park", "Park a project — save where you left off before switching to something else. Ends the current session and stores a parking note.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id":   prop("string", "Project ID to park"),
				"parking_note": prop("string", "Where you left off — what was in progress, what's next"),
				"session_id":   prop("string", "Session ID to end (from briefing or session start)"),
			},
			"required": []string{"project_id", "parking_note"},
		}),
		toolDef("waggle_log", "Log progress on a project. Use for significant milestones, completions, or status changes.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": prop("string", "Project ID"),
				"source":     prop("string", "Who/what made this progress: 'user', 'colony', 'cron', 'deploy', 'test'"),
				"summary":    prop("string", "Human-readable summary of what happened"),
				"detail":     prop("string", "Optional JSON detail payload"),
			},
			"required": []string{"project_id", "source", "summary"},
		}),
		toolDef("waggle_whats_next", "See all projects sorted by urgency. Shows what needs attention across your entire portfolio.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"account": prop("string", "Filter by account: 'pro' or 'team' (optional)"),
			},
		}),

		// --- Task tools ---
		toolDef("waggle_create_task", "Create a new task.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":       prop("string", "Task title"),
				"description": prop("string", "Detailed description"),
				"priority":    propEnum("string", []string{"critical", "high", "medium", "low"}, "Task priority"),
				"project_id":  prop("string", "Project ID this task belongs to"),
			},
			"required": []string{"title"},
		}),
		toolDef("waggle_list_tasks", "List tasks with optional filters.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status":     propEnum("string", []string{"backlog", "ready", "in_progress", "review", "done", "blocked"}, "Filter by status"),
				"priority":   propEnum("string", []string{"critical", "high", "medium", "low"}, "Filter by priority"),
				"project_id": prop("string", "Filter by project ID"),
			},
		}),
		toolDef("waggle_update_task", "Update an existing task.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          prop("string", "Task ID"),
				"status":      propEnum("string", []string{"backlog", "ready", "in_progress", "review", "done", "blocked"}, "New status"),
				"priority":    propEnum("string", []string{"critical", "high", "medium", "low"}, "New priority"),
				"title":       prop("string", "New title"),
				"description": prop("string", "New description"),
			},
			"required": []string{"id"},
		}),
		toolDef("waggle_complete_task", "Mark a task as done.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Task ID to complete")},
			"required":   []string{"id"},
		}),
		toolDef("waggle_delete_task", "Delete a task.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": prop("string", "Task ID to delete")},
			"required":   []string{"id"},
		}),

		// --- Project tools ---
		toolDef("waggle_list_projects", "List all projects.", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
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

		// --- Message tools ---
		toolDef("waggle_send_message", "Send a message to another agent or broadcast.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to":         prop("string", "Recipient (empty for broadcast)"),
				"body":       prop("string", "Message body"),
				"project_id": prop("string", "Project ID"),
			},
			"required": []string{"body"},
		}),
		toolDef("waggle_read_messages", "Read messages.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "Max messages to return"},
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
	// --- Context Manager tools ---

	case "waggle_ctx_briefing":
		projectID, _ := args["project_id"].(string)
		if projectID == "" {
			return nil, fmt.Errorf("project_id is required")
		}
		return a.get("/api/briefing?project_id=" + url.QueryEscape(projectID))

	case "waggle_park":
		projectID, _ := args["project_id"].(string)
		parkingNote, _ := args["parking_note"].(string)
		if projectID == "" || parkingNote == "" {
			return nil, fmt.Errorf("project_id and parking_note are required")
		}
		result, err := a.patchJSON("/api/projects/"+projectID, map[string]any{
			"parking_note": parkingNote,
		})
		if err != nil {
			return nil, err
		}
		if sessID, ok := args["session_id"].(string); ok && sessID != "" {
			a.postJSON("/api/ctx-sessions/"+sessID+"/end", map[string]string{
				"summary": parkingNote,
			})
		}
		return result, nil

	case "waggle_log":
		projectID, _ := args["project_id"].(string)
		source, _ := args["source"].(string)
		summary, _ := args["summary"].(string)
		if projectID == "" || source == "" || summary == "" {
			return nil, fmt.Errorf("project_id, source, and summary are required")
		}
		detail, _ := args["detail"].(string)
		return a.postJSON("/api/progress", map[string]string{
			"project_id": projectID,
			"source":     source,
			"summary":    summary,
			"detail":     detail,
		})

	case "waggle_whats_next":
		whatsNextURL := "/api/whats-next"
		if account, ok := args["account"].(string); ok && account != "" {
			whatsNextURL += "?account=" + url.QueryEscape(account)
		}
		return a.get(whatsNextURL)

	// --- Task tools ---

	case "waggle_create_task":
		return a.postJSON("/api/tasks", args)

	case "waggle_list_tasks":
		params := []string{}
		for _, key := range []string{"status", "priority", "project_id"} {
			if v, ok := args[key].(string); ok && v != "" {
				params = append(params, key+"="+url.QueryEscape(v))
			}
		}
		taskURL := "/api/tasks"
		if len(params) > 0 {
			taskURL += "?" + strings.Join(params, "&")
		}
		return a.get(taskURL)

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

	// --- Project tools ---

	case "waggle_list_projects":
		return a.get("/api/projects")

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

	// --- Message tools ---

	case "waggle_send_message":
		from := a.agentName
		if from == "" {
			from = "user"
		}
		to, _ := args["to"].(string)
		body, _ := args["body"].(string)
		if body == "" {
			return nil, fmt.Errorf("body is required")
		}
		projectID, _ := args["project_id"].(string)
		payload := map[string]string{
			"from": from,
			"to":   to,
			"body": body,
		}
		if projectID != "" {
			payload["project_id"] = projectID
		}
		return a.postJSON("/api/messages", payload)

	case "waggle_read_messages":
		msgURL := "/api/messages"
		params := []string{}
		if a.agentName != "" {
			params = append(params, "to="+url.QueryEscape(a.agentName))
		}
		if limit, ok := args["limit"].(float64); ok {
			params = append(params, fmt.Sprintf("limit=%d", int(limit)))
		}
		if len(params) > 0 {
			msgURL += "?" + strings.Join(params, "&")
		}
		return a.get(msgURL)

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
	a.outMu.Lock()
	fmt.Fprintf(a.out, "%s\n", data)
	a.outMu.Unlock()
}

func (a *Adapter) sendError(id any, code int, message, data string) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: message, Data: data},
	}
	d, _ := json.Marshal(resp)
	a.outMu.Lock()
	fmt.Fprintf(a.out, "%s\n", d)
	a.outMu.Unlock()
}

func (a *Adapter) sendNotification(method string, params any) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	data, _ := json.Marshal(msg)
	a.outMu.Lock()
	fmt.Fprintf(a.out, "%s\n", data)
	a.outMu.Unlock()
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

