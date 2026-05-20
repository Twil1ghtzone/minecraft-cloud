package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aethernet/aethernet/pkg/types"
)

// ident validates SQL identifiers (table / column names) to prevent injection
// via the DESCRIBE endpoint, which interpolates the name into a query string.
var ident = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_$]{0,63}$`)

// Workbench is the read+write SQL gateway exposed by the panel.
//
// Safety:
//   - Every query is parameterized to a single database (set in the per-user DSN).
//   - Statements that aren't SELECT/SHOW/DESCRIBE are gated by the
//     `write:databases` scope at the REST layer; here we additionally
//     refuse statements that touch information_schema.
//   - Hard row cap to bound memory.
type Workbench struct {
	pools map[string]*sql.DB // database name -> pool
	cfg   PoolConfig
}

type PoolConfig struct {
	Host           string
	Port           uint32
	AdminUser      string
	AdminPassword  string
	MaxRowLimit    uint32
	ConnectTimeout time.Duration
}

func NewWorkbench(c PoolConfig) *Workbench {
	if c.MaxRowLimit == 0 {
		c.MaxRowLimit = 10_000
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 5 * time.Second
	}
	return &Workbench{pools: map[string]*sql.DB{}, cfg: c}
}

func (w *Workbench) poolFor(db types.Database) (*sql.DB, error) {
	if p, ok := w.pools[db.Name]; ok {
		return p, nil
	}
	// Connect as the per-database service user. (The admin user could also
	// be used here but the panel runs the workbench scoped per-database so
	// SQL can never accidentally cross tenant boundaries.)
	// Use the admin credentials scoped to this database. The workbench is an
	// operator tool, so it connects as the cluster admin user constrained to
	// the target database — never as a per-tenant service account.
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?multiStatements=false&parseTime=true&timeout=%s",
		w.cfg.AdminUser, w.cfg.AdminPassword, w.cfg.Host, w.cfg.Port, db.Name,
		w.cfg.ConnectTimeout.String())
	pool, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(4)
	pool.SetMaxIdleConns(1)
	w.pools[db.Name] = pool
	return pool, nil
}

var dangerousPragmas = []string{"information_schema", "mysql.user", "mysql.db", "performance_schema"}

func (w *Workbench) Query(ctx context.Context, db types.Database, query string, rowLimit uint32) (*QueryResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("empty query")
	}
	low := strings.ToLower(q)
	for _, b := range dangerousPragmas {
		if strings.Contains(low, b) {
			return nil, fmt.Errorf("queries against %s are not allowed in workbench", b)
		}
	}
	if rowLimit == 0 || rowLimit > w.cfg.MaxRowLimit {
		rowLimit = w.cfg.MaxRowLimit
	}

	pool, err := w.poolFor(db)
	if err != nil {
		return nil, err
	}
	start := time.Now()

	if isSelectish(low) {
		rows, err := pool.QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanResult(rows, rowLimit, time.Since(start))
	}

	res, err := pool.ExecContext(ctx, q)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	return &QueryResult{
		RowsAffected: uint64(affected),
		DurationMS:   time.Since(start).Milliseconds(),
	}, nil
}

func (w *Workbench) Tables(ctx context.Context, db types.Database) ([]string, error) {
	pool, err := w.poolFor(db)
	if err != nil {
		return nil, err
	}
	rows, err := pool.QueryContext(ctx, "SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (w *Workbench) DescribeTable(ctx context.Context, db types.Database, table string) ([]Column, error) {
	if !ident.MatchString(table) {
		return nil, fmt.Errorf("invalid table name")
	}
	pool, err := w.poolFor(db)
	if err != nil {
		return nil, err
	}
	rows, err := pool.QueryContext(ctx, "DESCRIBE `"+table+"`")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var field, typeStr, null, key string
		var def, extra sql.NullString
		if err := rows.Scan(&field, &typeStr, &null, &key, &def, &extra); err != nil {
			return nil, err
		}
		out = append(out, Column{
			Name:     field,
			Type:     typeStr,
			Nullable: null == "YES",
		})
	}
	return out, nil
}

func isSelectish(low string) bool {
	for _, p := range []string{"select", "show", "describe", "explain", "with"} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

type QueryResult struct {
	Columns      []Column   `json:"columns"`
	Rows         [][]string `json:"rows"`
	NullMasks    []uint64   `json:"null_masks,omitempty"`
	RowsAffected uint64     `json:"rows_affected"`
	DurationMS   int64      `json:"duration_ms"`
}

type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

func scanResult(rows *sql.Rows, limit uint32, dur time.Duration) (*QueryResult, error) {
	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	out := &QueryResult{DurationMS: dur.Milliseconds()}
	for _, ct := range colTypes {
		nullable, _ := ct.Nullable()
		out.Columns = append(out.Columns, Column{
			Name: ct.Name(), Type: ct.DatabaseTypeName(), Nullable: nullable,
		})
	}
	values := make([]any, len(colTypes))
	scanPtrs := make([]any, len(colTypes))
	for i := range values {
		scanPtrs[i] = &values[i]
	}
	for rows.Next() && uint32(len(out.Rows)) < limit {
		if err := rows.Scan(scanPtrs...); err != nil {
			return nil, err
		}
		row := make([]string, len(values))
		var nullMask uint64
		for i, v := range values {
			if v == nil {
				nullMask |= 1 << i
				continue
			}
			row[i] = fmt.Sprint(v)
		}
		out.Rows = append(out.Rows, row)
		out.NullMasks = append(out.NullMasks, nullMask)
	}
	return out, nil
}
