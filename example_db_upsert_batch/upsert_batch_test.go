package exampledbupsertbatch

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestBuildUpsertQueryPostgres(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Dialect:         DialectPostgres,
		Table:           "asset_variants",
		InsertColumns:   []string{"logical_path", "source_etag", "webp_key"},
		ConflictColumns: []string{"logical_path"},
	}
	rows := []Row{
		{"images/a.jpg", "etag-a", "assets/a.webp"},
		{"images/b.jpg", "etag-b", "assets/b.webp"},
	}

	query, args, err := BuildUpsertQuery(cfg, rows)
	if err != nil {
		t.Fatalf("BuildUpsertQuery returned error: %v", err)
	}

	want := "INSERT INTO asset_variants (logical_path, source_etag, webp_key) VALUES ($1, $2, $3), ($4, $5, $6) ON CONFLICT (logical_path) DO UPDATE SET source_etag = excluded.source_etag, webp_key = excluded.webp_key"
	if query != want {
		t.Fatalf("unexpected query\nwant: %s\ngot:  %s", want, query)
	}
	if len(args) != 6 {
		t.Fatalf("unexpected args length: got %d", len(args))
	}
}

func TestBuildUpsertQueryMySQL(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Dialect:         DialectMySQL,
		Table:           "asset_variants",
		InsertColumns:   []string{"logical_path", "source_etag", "webp_key"},
		ConflictColumns: []string{"logical_path"},
	}
	rows := []Row{
		{"images/a.jpg", "etag-a", "assets/a.webp"},
	}

	query, _, err := BuildUpsertQuery(cfg, rows)
	if err != nil {
		t.Fatalf("BuildUpsertQuery returned error: %v", err)
	}

	if !strings.Contains(query, "ON DUPLICATE KEY UPDATE") {
		t.Fatalf("expected mysql upsert clause, got %s", query)
	}
	if !strings.Contains(query, "VALUES (?, ?, ?) AS new_values ON DUPLICATE KEY UPDATE") {
		t.Fatalf("expected mysql row alias in upsert query, got %s", query)
	}
	if !strings.Contains(query, "source_etag = new_values.source_etag") {
		t.Fatalf("expected mysql alias assignment, got %s", query)
	}
}

func TestUpsertRowsBatchesAndExecutesInParallel(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Dialect:         DialectSQLite,
		Table:           "asset_variants",
		InsertColumns:   []string{"logical_path", "source_etag", "webp_key"},
		ConflictColumns: []string{"logical_path"},
		BatchSize:       2,
		Workers:         3,
	}
	rows := []Row{
		{"images/a.jpg", "etag-a", "assets/a.webp"},
		{"images/b.jpg", "etag-b", "assets/b.webp"},
		{"images/c.jpg", "etag-c", "assets/c.webp"},
		{"images/d.jpg", "etag-d", "assets/d.webp"},
		{"images/e.jpg", "etag-e", "assets/e.webp"},
	}

	db := &fakeDB{}
	if err := UpsertRows(context.Background(), db, cfg, rows); err != nil {
		t.Fatalf("UpsertRows returned error: %v", err)
	}

	if got := len(db.calls); got != 3 {
		t.Fatalf("unexpected number of batch executions: got %d want 3", got)
	}
	if got := db.calls[0].argCount + db.calls[1].argCount + db.calls[2].argCount; got != 15 {
		t.Fatalf("unexpected total arg count: got %d want 15", got)
	}
}

func TestUpsertRowsReturnsExecutionError(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Dialect:         DialectSQLite,
		Table:           "asset_variants",
		InsertColumns:   []string{"logical_path", "source_etag"},
		ConflictColumns: []string{"logical_path"},
		BatchSize:       1,
		Workers:         2,
	}
	rows := []Row{
		{"images/a.jpg", "etag-a"},
		{"images/b.jpg", "etag-b"},
	}

	db := &fakeDB{failAt: 2}
	err := UpsertRows(context.Background(), db, cfg, rows)
	if err == nil {
		t.Fatal("expected UpsertRows to return an error")
	}
	if !strings.Contains(err.Error(), "upsert batch failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeDB struct {
	mu     sync.Mutex
	calls  []execCall
	failAt int
}

type execCall struct {
	query    string
	argCount int
}

func (db *fakeDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.calls = append(db.calls, execCall{
		query:    query,
		argCount: len(args),
	})
	if db.failAt > 0 && len(db.calls) == db.failAt {
		return fakeResult(0), errors.New("forced exec failure")
	}
	return fakeResult(len(args)), nil
}

type fakeResult int

func (r fakeResult) LastInsertId() (int64, error) {
	return 0, errors.New("not implemented")
}

func (r fakeResult) RowsAffected() (int64, error) {
	return int64(r), nil
}
