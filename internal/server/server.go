package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/maniginam/waggle/internal/api"
	"github.com/maniginam/waggle/internal/dashboard"
	"github.com/maniginam/waggle/internal/event"
	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/internal/store"
	"github.com/maniginam/waggle/internal/ws"
)

type Server struct {
	httpServer *http.Server
	store      *store.Store
	eventHub   *event.Hub
	wsHub      *ws.Hub
	api        *api.API
	stopReaper chan struct{}
}

type Config struct {
	Port   int
	DBPath string
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

	mux := http.NewServeMux()

	// Mount REST API
	apiHandler := restAPI.Handler()
	mux.Handle("/api/", apiHandler)

	// Mount WebSocket
	mux.Handle("/ws", wsHub.Handler())

	// Dashboard
	mux.Handle("/", dashboard.Handler())

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
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
	}, nil
}

func (s *Server) Start() error {
	s.stopReaper = make(chan struct{})
	go s.reapStaleAgents()
	go s.retentionCleanup()
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
			agents, err := s.store.ListAgents("")
			if err != nil {
				continue
			}
			cutoff := time.Now().UTC().Add(-staleTimeout)
			for _, agent := range agents {
				if agent.Status == model.AgentDisconnected {
					continue
				}
				if agent.LastSeen.Before(cutoff) {
					log.Printf("reaping stale agent: %s (last seen %s)", agent.Name, agent.LastSeen)
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
			if n, err := s.store.CleanupMessages(7); err == nil && n > 0 {
				log.Printf("cleaned up %d old read messages", n)
			}
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
