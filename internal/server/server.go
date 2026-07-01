package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maniginam/waggle/internal/api"
	"github.com/maniginam/waggle/internal/dashboard"
	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/mcp"
	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/internal/overseer"
	"github.com/maniginam/waggle/internal/store"
)

type Server struct {
	httpServer     *http.Server
	store          *store.Store
	eventHub       *event.Hub
	api            *api.API
	stopReaper     chan struct{}
	startedAt      time.Time
	overseerCancel context.CancelFunc
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
	restAPI := api.New(s, eh)

	startedAt := time.Now().UTC()
	mux := http.NewServeMux()

	// Mount REST API
	apiHandler := restAPI.Handler()
	mux.Handle("/api/", apiHandler)

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
		uptime := time.Since(startedAt).Round(time.Second).String()
		data, _ := json.Marshal(map[string]any{
			"status":     "ok",
			"version":    cfg.Version,
			"uptime":     uptime,
			"started_at": startedAt.Format(time.RFC3339),
			"sse_subs":   eh.SubscriberCount(),
		})
		w.Write(data)
	})

	// Version endpoint
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(map[string]string{"version": cfg.Version})
		w.Write(data)
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
		api:        restAPI,
		startedAt:  startedAt,
	}, nil
}

func (s *Server) Start() error {
	s.stopReaper = make(chan struct{})
	go s.reapStaleAgents()
	go s.retentionCleanup()

	if os.Getenv("OVERSEER_ENABLED") == "true" {
		ov := overseer.New(s.store, s.eventHub)

		colonyPath := os.Getenv("COLONY_DB_PATH")
		if colonyPath == "" {
			colonyPath = "~/.colony/colony.db"
		}
		ov.Register(overseer.NewColonySource(expandHome(colonyPath)), interval("OVERSEER_COLONY_INTERVAL", 3*time.Second))

		gtBin := os.Getenv("GASTOWN_BIN")
		if gtBin == "" {
			gtBin = "gt"
		}
		ov.Register(overseer.NewGasTownSource(gtBin), interval("OVERSEER_GASTOWN_INTERVAL", 10*time.Second))

		ctx, cancel := context.WithCancel(context.Background())
		s.overseerCancel = cancel
		go ov.Run(ctx)
	}

	log.Printf("waggle server listening on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("shutting down waggle server...")
	if s.overseerCancel != nil {
		s.overseerCancel()
	}
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
			log.Printf("reaping stale agent: %s (last seen %s)", agent.Name, agent.LastSeen)
			staleEvt := &model.Event{
				Type:    model.EventAgentStale,
				AgentID: agent.Name,
				Payload: map[string]any{
					"agent_name": agent.Name,
					"last_seen":  agent.LastSeen.Format("2006-01-02T15:04:05Z"),
				},
			}
			s.store.RecordEvent(staleEvt)
			s.eventHub.Publish(staleEvt)
			if err := s.store.DisconnectAgent(agent.Name); err != nil {
				log.Printf("failed to disconnect agent %s: %v", agent.Name, err)
			}
			leftEvt := &model.Event{
				Type:    model.EventAgentLeft,
				AgentID: agent.Name,
			}
			s.store.RecordEvent(leftEvt)
			s.eventHub.Publish(leftEvt)
		}
	}
	// Purge agents disconnected for 24+ hours
	if purged, err := s.store.PurgeStaleAgents(24 * time.Hour); err == nil && purged > 0 {
		log.Printf("purged %d agents disconnected for 24+ hours", purged)
	}
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
			if n, err := s.store.CleanupMessages(30); err == nil && n > 0 {
				log.Printf("cleaned up %d old read messages", n)
			}
			if n, err := s.store.CleanupStaleTasks(14); err == nil && n > 0 {
				log.Printf("auto-closed %d stale tasks (no updates for 14+ days)", n)
			}
		}
	}
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func interval(env string, def time.Duration) time.Duration {
	if v := os.Getenv(env); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
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
