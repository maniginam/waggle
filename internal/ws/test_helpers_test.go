package ws

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestMux(hub *Hub) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/ws", hub.Handler())
	return mux
}

func newTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts.URL
}
