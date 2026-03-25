package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maniginam/waggle/internal/api"
	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/store"
)

func setupSSE(t *testing.T) (*SSEHandler, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	eh := event.NewHub()
	restAPI := api.New(s, eh)
	apiServer := httptest.NewServer(restAPI.Handler())
	t.Cleanup(apiServer.Close)

	handler := NewSSEHandler(apiServer.URL)
	return handler, apiServer
}

func TestSSEHandler_Initialize(t *testing.T) {
	handler, _ := setupSSE(t)
	sseServer := httptest.NewServer(handler.Handler())
	defer sseServer.Close()

	// Connect to SSE endpoint
	resp, err := http.Get(sseServer.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}

	// Read the endpoint event (first message tells client where to POST)
	scanner := bufio.NewScanner(resp.Body)
	var endpointURL string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			endpointURL = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	if endpointURL == "" {
		t.Fatal("expected endpoint URL in first SSE message")
	}
	if !strings.Contains(endpointURL, "/message") {
		t.Errorf("expected endpoint URL to contain /message, got %s", endpointURL)
	}

	// Send initialize request via POST
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
		},
	}
	body, _ := json.Marshal(initReq)
	postResp, err := http.Post(endpointURL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d", postResp.StatusCode)
	}

	// Read the response from SSE stream
	var response map[string]any
	deadline := time.After(2 * time.Second)
	done := make(chan bool)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if err := json.Unmarshal([]byte(data), &response); err == nil {
					if _, hasResult := response["result"]; hasResult {
						done <- true
						return
					}
				}
			}
		}
	}()

	select {
	case <-done:
	case <-deadline:
		t.Fatal("timed out waiting for SSE response")
	}

	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %v", response)
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("expected %s, got %v", protocolVersion, result["protocolVersion"])
	}
}

func TestSSEHandler_ToolsList(t *testing.T) {
	handler, _ := setupSSE(t)
	sseServer := httptest.NewServer(handler.Handler())
	defer sseServer.Close()

	// Connect to SSE
	resp, err := http.Get(sseServer.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Get endpoint URL
	scanner := bufio.NewScanner(resp.Body)
	var endpointURL string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			endpointURL = strings.TrimPrefix(line, "data: ")
			break
		}
	}

	// Send tools/list
	req := map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}
	body, _ := json.Marshal(req)
	http.Post(endpointURL, "application/json", bytes.NewReader(body))

	// Read response
	var response map[string]any
	deadline := time.After(2 * time.Second)
	done := make(chan bool)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if err := json.Unmarshal([]byte(data), &response); err == nil {
					if _, hasResult := response["result"]; hasResult {
						done <- true
						return
					}
				}
			}
		}
	}()

	select {
	case <-done:
	case <-deadline:
		t.Fatal("timed out waiting for tools/list response")
	}

	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) < 10 {
		t.Errorf("expected at least 10 tools, got %d", len(tools))
	}
}

func TestSSEHandler_RegisterAndCreateTask(t *testing.T) {
	handler, _ := setupSSE(t)
	sseServer := httptest.NewServer(handler.Handler())
	defer sseServer.Close()

	// Connect
	resp, err := http.Get(sseServer.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var endpointURL string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			endpointURL = strings.TrimPrefix(line, "data: ")
			break
		}
	}

	// Register agent
	regReq := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "waggle_register_agent",
			"arguments": map[string]any{"name": "sse-agent", "type": "claude-code"},
		},
	}
	body, _ := json.Marshal(regReq)
	http.Post(endpointURL, "application/json", bytes.NewReader(body))

	// Wait for register response
	waitForResponse(t, scanner, 2*time.Second)

	// Create task
	taskReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "waggle_create_task",
			"arguments": map[string]any{"title": "SSE task", "priority": "high"},
		},
	}
	body, _ = json.Marshal(taskReq)
	http.Post(endpointURL, "application/json", bytes.NewReader(body))

	response := waitForResponse(t, scanner, 2*time.Second)
	result := response["result"].(map[string]any)
	if result["isError"] != nil && result["isError"].(bool) {
		t.Fatalf("create task failed: %v", result)
	}
}

func TestSSEHandler_InvalidSession(t *testing.T) {
	handler, _ := setupSSE(t)
	sseServer := httptest.NewServer(handler.Handler())
	defer sseServer.Close()

	// POST to message endpoint with invalid session
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}
	body, _ := json.Marshal(req)
	resp, err := http.Post(sseServer.URL+"/message?session_id=invalid", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for invalid session, got %d", resp.StatusCode)
	}
}

func TestSSEHandler_Ping(t *testing.T) {
	handler, _ := setupSSE(t)
	sseServer := httptest.NewServer(handler.Handler())
	defer sseServer.Close()

	resp, err := http.Get(sseServer.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var endpointURL string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			endpointURL = strings.TrimPrefix(line, "data: ")
			break
		}
	}

	pingReq := map[string]any{"jsonrpc": "2.0", "id": 99, "method": "ping"}
	body, _ := json.Marshal(pingReq)
	http.Post(endpointURL, "application/json", bytes.NewReader(body))

	response := waitForResponse(t, scanner, 2*time.Second)
	if response["result"] == nil {
		t.Error("expected result for ping")
	}
}

func waitForResponse(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) map[string]any {
	t.Helper()
	var response map[string]any
	deadline := time.After(timeout)
	done := make(chan map[string]any, 1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				var resp map[string]any
				if err := json.Unmarshal([]byte(data), &resp); err == nil {
					if _, hasResult := resp["result"]; hasResult {
						done <- resp
						return
					}
					if _, hasError := resp["error"]; hasError {
						done <- resp
						return
					}
				}
			}
		}
	}()

	select {
	case response = <-done:
	case <-deadline:
		t.Fatal("timed out waiting for SSE response")
	}
	return response
}
