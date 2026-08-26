package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func createTestDB(t *testing.T, base, name string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name})
	res, _ := http.Post(base+"/api/udb", "application/json", bytes.NewReader(body))
	var db map[string]any
	json.NewDecoder(res.Body).Decode(&db)
	return db["id"].(string)
}

func addTestColumn(t *testing.T, base, dbID string, m map[string]any) string {
	t.Helper()
	body, _ := json.Marshal(m)
	res, _ := http.Post(base+"/api/udb/"+dbID+"/columns", "application/json", bytes.NewReader(body))
	var c map[string]any
	json.NewDecoder(res.Body).Decode(&c)
	return c["id"].(string)
}

func TestUDBColumnEndpoints(t *testing.T) {
	_, srv := setup(t)
	dbID := createTestDB(t, srv.URL, "T")

	// add column
	body, _ := json.Marshal(map[string]any{"name": "Status", "type": "select",
		"options": []map[string]any{{"value": "Todo"}}})
	res, _ := http.Post(srv.URL+"/api/udb/"+dbID+"/columns", "application/json", bytes.NewReader(body))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("add column status %d", res.StatusCode)
	}

	// bad type -> 400 invalid_column_type
	bad, _ := json.Marshal(map[string]any{"name": "X", "type": "bogus"})
	res, _ = http.Post(srv.URL+"/api/udb/"+dbID+"/columns", "application/json", bytes.NewReader(bad))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad type status %d", res.StatusCode)
	}

	// list
	res, _ = http.Get(srv.URL + "/api/udb/" + dbID + "/columns")
	var cols []map[string]any
	json.NewDecoder(res.Body).Decode(&cols)
	if len(cols) != 1 {
		t.Fatalf("expected 1 column, got %d", len(cols))
	}
}

func TestUDBRowEndpoints(t *testing.T) {
	_, srv := setup(t)
	dbID := createTestDB(t, srv.URL, "T")
	nameCol := addTestColumn(t, srv.URL, dbID, map[string]any{"name": "Name", "type": "text"})
	ageCol := addTestColumn(t, srv.URL, dbID, map[string]any{"name": "Age", "type": "number"})

	// create row
	body, _ := json.Marshal(map[string]any{"data": map[string]any{nameCol: "Ada", ageCol: 36}})
	res, _ := http.Post(srv.URL+"/api/udb/"+dbID+"/rows", "application/json", bytes.NewReader(body))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create row status %d", res.StatusCode)
	}
	var row map[string]any
	json.NewDecoder(res.Body).Decode(&row)
	rowID := row["id"].(string)

	// wrong type -> 400 invalid_cell
	badBody, _ := json.Marshal(map[string]any{"data": map[string]any{ageCol: "old"}})
	res, _ = http.Post(srv.URL+"/api/udb/"+dbID+"/rows", "application/json", bytes.NewReader(badBody))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad cell, got %d", res.StatusCode)
	}

	// list
	res, _ = http.Get(srv.URL + "/api/udb/" + dbID + "/rows")
	var rows []map[string]any
	json.NewDecoder(res.Body).Decode(&rows)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	// patch
	patch, _ := json.Marshal(map[string]any{"data": map[string]any{nameCol: "Grace"}})
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/udb/"+dbID+"/rows/"+rowID, bytes.NewReader(patch))
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d", res.StatusCode)
	}

	// delete
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/udb/"+dbID+"/rows/"+rowID, nil)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", res.StatusCode)
	}
}

func TestUDBViewEndpoints(t *testing.T) {
	_, srv := setup(t)
	defer srv.Close()
	dbID := createTestDB(t, srv.URL, "T")
	selCol := addTestColumn(t, srv.URL, dbID, map[string]any{"name": "Stage", "type": "select",
		"options": []map[string]any{{"value": "A"}}})

	// table view
	body, _ := json.Marshal(map[string]any{"name": "All", "kind": "table"})
	res, _ := http.Post(srv.URL+"/api/udb/"+dbID+"/views", "application/json", bytes.NewReader(body))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("table view status %d", res.StatusCode)
	}

	// kanban with valid select group -> 201
	kb, _ := json.Marshal(map[string]any{"name": "Board", "kind": "kanban",
		"config": map[string]any{"group_by": selCol}})
	res, _ = http.Post(srv.URL+"/api/udb/"+dbID+"/views", "application/json", bytes.NewReader(kb))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("kanban view status %d", res.StatusCode)
	}

	// calendar without a date field -> 400 invalid_view_config
	cal, _ := json.Marshal(map[string]any{"name": "Cal", "kind": "calendar",
		"config": map[string]any{"date_field": selCol}})
	res, _ = http.Post(srv.URL+"/api/udb/"+dbID+"/views", "application/json", bytes.NewReader(cal))
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad calendar, got %d", res.StatusCode)
	}

	// list
	res, _ = http.Get(srv.URL + "/api/udb/" + dbID + "/views")
	var views []map[string]any
	json.NewDecoder(res.Body).Decode(&views)
	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}
}

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
