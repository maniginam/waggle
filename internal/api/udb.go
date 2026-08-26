package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/internal/store"
)

// udbMapErr maps store sentinel errors to HTTP responses.
// Returns true if it wrote a response.
func udbMapErr(w http.ResponseWriter, err error) bool {
	switch err {
	case nil:
		return false
	case store.ErrNotFound:
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case store.ErrUnknownColumn:
		writeError(w, http.StatusBadRequest, "unknown_column", err.Error())
	case store.ErrInvalidCell:
		writeError(w, http.StatusBadRequest, "invalid_cell", err.Error())
	case store.ErrInvalidColumnType:
		writeError(w, http.StatusBadRequest, "invalid_column_type", err.Error())
	case store.ErrInvalidView:
		writeError(w, http.StatusBadRequest, "invalid_view", err.Error())
	case store.ErrInvalidViewConfig:
		writeError(w, http.StatusBadRequest, "invalid_view_config", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
	}
	return true
}

func (a *API) handleUDB(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		dbs, err := a.store.ListDatabases(r.URL.Query().Get("project_id"))
		if udbMapErr(w, err) {
			return
		}
		if dbs == nil {
			dbs = []*model.Database{}
		}
		writeJSON(w, http.StatusOK, dbs)
	case http.MethodPost:
		var d model.Database
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if d.Name == "" {
			writeError(w, http.StatusBadRequest, "missing_name", "name is required")
			return
		}
		if err := a.store.CreateDatabase(&d); udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseCreated, Payload: d})
		writeJSON(w, http.StatusCreated, d)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleUDBItem routes /api/udb/{id} and /api/udb/{id}/{sub}...
// Sub-resources (columns, rows, views) are added in Tasks 7-9.
func (a *API) handleUDBItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/udb/")
	parts := strings.Split(rest, "/")
	dbID := parts[0]
	if dbID == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "database ID required")
		return
	}
	if len(parts) > 1 {
		switch parts[1] {
		case "columns":
			a.handleUDBColumns(w, r, dbID, parts[2:])
			return
		case "rows":
			a.handleUDBRows(w, r, dbID, parts[2:])
			return
		case "views":
			a.handleUDBViews(w, r, dbID, parts[2:])
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "unknown sub-resource")
		return
	}
	switch r.Method {
	case http.MethodGet:
		d, err := a.store.GetDatabase(dbID)
		if udbMapErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, d)
	case http.MethodPatch:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		d, err := a.store.RenameDatabase(dbID, body.Name)
		if udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseUpdated, Payload: d})
		writeJSON(w, http.StatusOK, d)
	case http.MethodDelete:
		if err := a.store.DeleteDatabase(dbID); udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseDeleted, Payload: map[string]string{"id": dbID}})
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) handleUDBColumns(w http.ResponseWriter, r *http.Request, dbID string, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		switch r.Method {
		case http.MethodGet:
			cols, err := a.store.ListColumns(dbID)
			if udbMapErr(w, err) {
				return
			}
			if cols == nil {
				cols = []*model.Column{}
			}
			writeJSON(w, http.StatusOK, cols)
		case http.MethodPost:
			var c model.Column
			if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			if c.Name == "" {
				writeError(w, http.StatusBadRequest, "missing_name", "column name is required")
				return
			}
			c.DBID = dbID
			if err := a.store.AddColumn(&c); udbMapErr(w, err) {
				return
			}
			a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
			writeJSON(w, http.StatusCreated, c)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	colID := rest[0]
	switch r.Method {
	case http.MethodPatch:
		var updates map[string]any
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		c, err := a.store.UpdateColumn(colID, updates)
		if udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		writeJSON(w, http.StatusOK, c)
	case http.MethodDelete:
		if err := a.store.DeleteColumn(colID); udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (a *API) handleUDBRows(w http.ResponseWriter, r *http.Request, dbID string, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		switch r.Method {
		case http.MethodGet:
			rows, err := a.store.ListRows(dbID)
			if udbMapErr(w, err) {
				return
			}
			if rows == nil {
				rows = []*model.Row{}
			}
			writeJSON(w, http.StatusOK, rows)
		case http.MethodPost:
			var body struct {
				Data map[string]any `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			row := model.Row{DBID: dbID, Data: body.Data}
			if err := a.store.CreateRow(&row); udbMapErr(w, err) {
				return
			}
			a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
			writeJSON(w, http.StatusCreated, row)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	rowID := rest[0]
	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		row, err := a.store.UpdateRow(rowID, body.Data)
		if udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		writeJSON(w, http.StatusOK, row)
	case http.MethodDelete:
		if err := a.store.DeleteRow(rowID); udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (a *API) handleUDBViews(w http.ResponseWriter, r *http.Request, dbID string, rest []string) {
	if len(rest) == 0 || rest[0] == "" {
		switch r.Method {
		case http.MethodGet:
			views, err := a.store.ListViews(dbID)
			if udbMapErr(w, err) {
				return
			}
			if views == nil {
				views = []*model.View{}
			}
			writeJSON(w, http.StatusOK, views)
		case http.MethodPost:
			var v model.View
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			if v.Name == "" {
				writeError(w, http.StatusBadRequest, "missing_name", "view name is required")
				return
			}
			v.DBID = dbID
			if err := a.store.CreateView(&v); udbMapErr(w, err) {
				return
			}
			a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
			writeJSON(w, http.StatusCreated, v)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	viewID := rest[0]
	switch r.Method {
	case http.MethodPatch:
		var updates map[string]any
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		v, err := a.store.UpdateView(viewID, updates)
		if udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		if err := a.store.DeleteView(viewID); udbMapErr(w, err) {
			return
		}
		a.emit(&model.Event{Type: model.EventDatabaseChanged, Payload: map[string]string{"db_id": dbID}})
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
