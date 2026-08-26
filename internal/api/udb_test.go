package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestUDBDatabaseEndpoints(t *testing.T) {
	_, srv := setup(t)

	// create
	body, _ := json.Marshal(map[string]any{"name": "Contacts", "project_id": "p1"})
	res, _ := http.Post(srv.URL+"/api/udb", "application/json", bytes.NewReader(body))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d", res.StatusCode)
	}
	var db map[string]any
	json.NewDecoder(res.Body).Decode(&db)
	id, _ := db["id"].(string)
	if id == "" {
		t.Fatal("no id returned")
	}

	// list
	res, _ = http.Get(srv.URL + "/api/udb?project_id=p1")
	var list []map[string]any
	json.NewDecoder(res.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("list len %d", len(list))
	}

	// delete
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/udb/"+id, nil)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", res.StatusCode)
	}

	// get 404
	res, _ = http.Get(srv.URL + "/api/udb/" + id)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.StatusCode)
	}
}
