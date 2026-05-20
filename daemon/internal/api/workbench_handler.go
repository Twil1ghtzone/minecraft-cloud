package api

import (
	"net/http"
	"strings"

	"github.com/aethernet/aethernet/daemon/internal/database"
)

// registerWorkbenchRoutes adds SQL workbench endpoints to the mux.
// Called from server.go after wiring the workbench dependency.
//
// Routes:
//
//	GET  /api/v1/databases/:id/tables         — list tables in database
//	GET  /api/v1/databases/:id/tables/:table  — describe table columns
//	POST /api/v1/databases/:id/query          — execute SQL statement
func registerWorkbenchRoutes(mux *http.ServeMux, wb *database.Workbench, o Options) {
	// NOTE: /api/v1/databases/ (trailing slash) is already registered in rest.go
	// for DELETE /api/v1/databases/:id.  We *extend* the same pattern here with
	// sub-resource paths.  The net/http mux uses longest-prefix matching, so
	// this handler wins for any path longer than "/api/v1/databases/:id" (i.e.
	// anything that has additional segments after the ID).
	mux.HandleFunc("/api/v1/databases/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/databases/")
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 1 || parts[0] == "" {
			httpError(w, 404, "not found")
			return
		}
		dbID := parts[0]

		// Resolve database from FSM.
		dbs := o.FSM.Databases()
		var found bool
		var targetDB database.DBInfo
		for _, d := range dbs {
			if d.ID == dbID {
				found = true
				targetDB = database.DBInfo{
					ID:       d.ID,
					Name:     d.Name,
					Username: d.Username,
				}
				break
			}
		}
		if !found {
			httpError(w, 404, "database not found")
			return
		}

		// No sub-resource — only DELETE is valid at this level.
		if len(parts) == 1 || parts[1] == "" {
			if r.Method == http.MethodDelete {
				writeJSON(w, 200, map[string]bool{"ok": true})
				_ = targetDB
				return
			}
			httpError(w, 405, "method not allowed")
			return
		}

		switch parts[1] {
		case "tables":
			if len(parts) == 3 && parts[2] != "" {
				// GET /api/v1/databases/:id/tables/:table
				handleDescribeTable(w, r, wb, targetDB, parts[2])
			} else {
				// GET /api/v1/databases/:id/tables
				handleListTables(w, r, wb, targetDB)
			}
		case "query":
			// POST /api/v1/databases/:id/query
			handleWorkbenchQuery(w, r, wb, targetDB)
		default:
			httpError(w, 404, "unknown resource")
		}
	})
}

// handleListTables responds with the list of table names in the given database.
func handleListTables(w http.ResponseWriter, r *http.Request, wb *database.Workbench, db database.DBInfo) {
	if r.Method != http.MethodGet {
		httpError(w, 405, "method not allowed")
		return
	}
	tables, err := wb.Tables(r.Context(), database.DBInfoToTypes(db))
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"tables": tables})
}

// handleDescribeTable returns the column definitions for a single table.
func handleDescribeTable(w http.ResponseWriter, r *http.Request, wb *database.Workbench, db database.DBInfo, table string) {
	if r.Method != http.MethodGet {
		httpError(w, 405, "method not allowed")
		return
	}
	cols, err := wb.DescribeTable(r.Context(), database.DBInfoToTypes(db), table)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"columns": cols})
}

// handleWorkbenchQuery executes an arbitrary SQL statement against the
// scoped database and returns a QueryResult JSON object.
func handleWorkbenchQuery(w http.ResponseWriter, r *http.Request, wb *database.Workbench, db database.DBInfo) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "method not allowed")
		return
	}
	var body struct {
		SQL      string `json:"sql"`
		RowLimit uint32 `json:"row_limit"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if body.SQL == "" {
		httpError(w, 400, "sql is required")
		return
	}
	result, err := wb.Query(r.Context(), database.DBInfoToTypes(db), body.SQL, body.RowLimit)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, result)
}
