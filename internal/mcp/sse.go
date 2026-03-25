package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/maniginam/waggle/pkg/id"
)

// SSEHandler provides MCP over HTTP with SSE transport.
// Clients connect via GET /sse to receive a Server-Sent Events stream,
// then POST JSON-RPC requests to the /message endpoint returned in the
// initial SSE "endpoint" event.
type SSEHandler struct {
	baseURL  string
	mu       sync.RWMutex
	sessions map[string]*sseSession
}

type sseSession struct {
	id      string
	adapter *Adapter
	send    chan []byte
	done    chan struct{}
}

func NewSSEHandler(baseURL string) *SSEHandler {
	return &SSEHandler{
		baseURL:  baseURL,
		sessions: make(map[string]*sseSession),
	}
}

func (h *SSEHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", h.handleSSE)
	mux.HandleFunc("/message", h.handleMessage)
	return mux
}

func (h *SSEHandler) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	sessionID := id.New()
	sess := &sseSession{
		id:   sessionID,
		send: make(chan []byte, 64),
		done: make(chan struct{}),
	}

	// Create an adapter that writes to the session's send channel
	adapter := NewAdapter(h.baseURL)
	adapter.out = &sseWriter{send: sess.send}
	sess.adapter = adapter

	h.mu.Lock()
	h.sessions[sessionID] = sess
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.sessions, sessionID)
		h.mu.Unlock()
		close(sess.done)
		adapter.StopHeartbeat()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send the endpoint event telling the client where to POST
	// Use the request's host to build the message URL
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	messageURL := fmt.Sprintf("%s://%s/message?session_id=%s", scheme, host, sessionID)

	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", messageURL)
	flusher.Flush()

	for {
		select {
		case data, ok := <-sess.send:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (h *SSEHandler) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	h.mu.RLock()
	sess, ok := h.sessions[sessionID]
	h.mu.RUnlock()
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	var req jsonrpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		sess.adapter.sendError(nil, -32700, "Parse error", err.Error())
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Process the request asynchronously; response goes via SSE
	go sess.adapter.handleRequest(&req)

	w.WriteHeader(http.StatusAccepted)
}

// sseWriter adapts io.Writer to send data to the SSE session channel.
type sseWriter struct {
	send chan []byte
}

func (w *sseWriter) Write(p []byte) (int, error) {
	// The adapter writes full JSON-RPC responses as single lines.
	// Buffer until we get a complete line (ending with \n).
	lines := bytes.Split(p, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		data := make([]byte, len(line))
		copy(data, line)
		select {
		case w.send <- data:
		default:
			// Drop if slow
		}
	}
	return len(p), nil
}

func (h *SSEHandler) SessionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}
