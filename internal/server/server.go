package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"time"

	"github.com/maniginam/waggle/internal/api"
	"github.com/maniginam/waggle/internal/dashboard"
	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/mcp"
	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/internal/push"
	"github.com/maniginam/waggle/internal/store"
	"github.com/maniginam/waggle/internal/ws"
)

type Server struct {
	httpServer *http.Server
	store      *store.Store
	eventHub   *event.Hub
	wsHub      *ws.Hub
	api        *api.API
	push       *push.Notifier
	stopReaper chan struct{}
}

type Config struct {
	Port    int
	DBPath  string
	Version string
}

func New(cfg Config) (*Server, error) {
	if cfg.Port == 0 {
		cfg.Port = 4740
	}
	if cfg.DBPath == "" {
		cfg.DBPath = store.DefaultPath()
	}

	s, err := store.New(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}

	eh := event.NewHub()
	wsHub := ws.NewHub(s, eh)
	restAPI := api.New(s, eh)

	var pushNotifier *push.Notifier
	if p, err := push.NewNotifier(s); err == nil {
		pushNotifier = p
	} else {
		log.Printf("push notifications disabled: %v", err)
	}

	mux := http.NewServeMux()

	// Mount REST API
	apiHandler := restAPI.Handler()
	mux.Handle("/api/", apiHandler)

	// Mount WebSocket
	mux.Handle("/ws", wsHub.Handler())

	// Mount MCP SSE transport
	baseURL := fmt.Sprintf("http://localhost:%d", cfg.Port)
	mcpSSE := mcp.NewSSEHandler(baseURL)
	mux.Handle("/mcp/sse", http.StripPrefix("/mcp", mcpSSE.Handler()))
	mux.Handle("/mcp/message", http.StripPrefix("/mcp", mcpSSE.Handler()))

	// Dashboard
	mux.Handle("/", dashboard.Handler())

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Version endpoint
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"version":"%s"}`, cfg.Version)))
	})

	srv := &http.Server{
		Addr:        fmt.Sprintf(":%d", cfg.Port),
		Handler:     cors(mux),
		ReadTimeout: 15 * time.Second,
		// No WriteTimeout — SSE and WebSocket connections are long-lived.
		// Individual handlers can enforce their own deadlines.
		IdleTimeout: 120 * time.Second,
	}

	return &Server{
		httpServer: srv,
		store:      s,
		eventHub:   eh,
		wsHub:      wsHub,
		api:        restAPI,
		push:       pushNotifier,
	}, nil
}

func (s *Server) Start() error {
	s.stopReaper = make(chan struct{})
	go s.reapStaleAgents()
	go s.retentionCleanup()
	if s.push != nil {
		go s.pushNotificationLoop()
	}
	log.Printf("waggle server listening on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("shutting down waggle server...")
	close(s.stopReaper)
	err := s.httpServer.Shutdown(ctx)
	s.store.Close()
	return err
}

const staleTimeout = 90 * time.Second

func (s *Server) reapStaleAgents() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopReaper:
			return
		case <-ticker.C:
			s.reapAgentsStaleBefore(time.Now().UTC().Add(-staleTimeout))
		}
	}
}

func (s *Server) reapAgentsStaleBefore(cutoff time.Time) {
	agents, err := s.store.ListAgents("")
	if err != nil {
		return
	}
	for _, agent := range agents {
		if agent.Status == model.AgentDisconnected {
			continue
		}
		if agent.LastSeen.Before(cutoff) {
			// Check if agent has an active tmux session before reaping
			sessionName := "waggle-" + agent.Name
			if tmuxSessionAlive(sessionName) {
				// Agent has a live tmux session — refresh its heartbeat
				s.store.TouchAgent(agent.Name)
				continue
			}
			log.Printf("reaping stale agent: %s (last seen %s)", agent.Name, agent.LastSeen)
			// Emit stale event before disconnecting (for push notifications)
			s.eventHub.Publish(&model.Event{
				Type:    model.EventAgentStale,
				AgentID: agent.Name,
				Payload: map[string]any{
					"agent_name": agent.Name,
					"last_seen":  agent.LastSeen.Format("2006-01-02T15:04:05Z"),
				},
			})
			s.store.DisconnectAgent(agent.Name)
			s.eventHub.Publish(&model.Event{
				Type:    model.EventAgentLeft,
				AgentID: agent.Name,
			})
		}
	}
	// Purge agents disconnected for 24+ hours
	if purged, err := s.store.PurgeStaleAgents(24 * time.Hour); err == nil && purged > 0 {
		log.Printf("purged %d agents disconnected for 24+ hours", purged)
	}
}

// tmuxSessionAlive checks if a tmux session exists and has a running process (not just a shell).
func tmuxSessionAlive(sessionName string) bool {
	// Check if session exists
	if err := exec.Command("tmux", "has-session", "-t", sessionName).Run(); err != nil {
		return false
	}
	// Check if there's a claude process in the session
	out, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_current_command}").Output()
	if err != nil {
		return false
	}
	cmd := string(out)
	// If the pane is running claude or a launch script, the agent is alive
	return cmd != "" && cmd != "zsh\n" && cmd != "bash\n" && cmd != "sh\n"
}

func (s *Server) retentionCleanup() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopReaper:
			return
		case <-ticker.C:
			if n, err := s.store.CleanupEvents(30); err == nil && n > 0 {
				log.Printf("cleaned up %d old events", n)
			}
			if n, err := s.store.CleanupMessages(7); err == nil && n > 0 {
				log.Printf("cleaned up %d old read messages", n)
			}
			if n, err := s.store.CleanupStaleTasks(14); err == nil && n > 0 {
				log.Printf("auto-closed %d stale tasks (no updates for 14+ days)", n)
			}
		}
	}
}

func (s *Server) pushNotificationLoop() {
	sub := s.eventHub.Subscribe("", "")
	defer s.eventHub.Unsubscribe(sub)

	pushEvents := map[model.EventType]string{
		model.EventTaskCompleted:       "Task completed",
		model.EventTaskClaimed:         "Task claimed",
		model.EventMessage:             "New message",
		model.EventAgentJoined:         "Agent connected",
		model.EventAgentLeft:           "Agent disconnected",
		model.EventAgentStale:          "Agent heartbeat lost",
		"review_submitted":             "Review needs attention",
	}

	for {
		select {
		case evt, ok := <-sub.Ch:
			if !ok {
				return
			}
			title, shouldPush := pushEvents[evt.Type]
			if !shouldPush {
				continue
			}
			body := ""
			// Extract richer details from payload
			if payload, ok := evt.Payload.(map[string]any); ok {
				if msgBody, ok := payload["body"].(string); ok && evt.Type == model.EventMessage {
					from, _ := payload["from"].(string)
					if from != "" {
						body = from + ": " + msgBody
					} else {
						body = msgBody
					}
				} else if taskTitle, ok := payload["title"].(string); ok {
					body = taskTitle
				}
			}
			if body == "" {
				if evt.AgentID != "" {
					body = evt.AgentID
				}
				if evt.TaskID != "" {
					if body != "" {
						body += " — "
					}
					body += evt.TaskID
				}
			}
			s.push.Send(push.PushPayload{
				Title: title,
				Body:  body,
				Tag:   string(evt.Type),
				URL:   "/",
			})
		case <-s.stopReaper:
			return
		}
	}
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Store() *store.Store {
	return s.store
}

func (s *Server) EventHub() *event.Hub {
	return s.eventHub
}
