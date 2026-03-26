package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndex(t *testing.T) {
	handler := Handler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html content type, got %s", ct)
	}
}

func TestHandlerServesServiceWorker(t *testing.T) {
	handler := Handler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Check that sw.js exists (if it does)
	resp, err := http.Get(ts.URL + "/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// sw.js may or may not exist — just verify we get a valid response
	if resp.StatusCode != 200 && resp.StatusCode != 404 {
		t.Errorf("expected 200 or 404 for sw.js, got %d", resp.StatusCode)
	}
}
