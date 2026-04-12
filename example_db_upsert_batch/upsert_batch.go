package exampledbupsertbatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
)

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectMySQL    Dialect = "mysql"
	DialectSQLite   Dialect = "sqlite"
)

type Row []any

type Config struct {
	Dialect         Dialect
	Table           string
	InsertColumns   []string
	ConflictColumns []string
	UpdateColumns   []string
	BatchSize       int
	Workers         int
}

type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func UpsertRows(ctx context.Context, db Execer, cfg Config, rows []Row) error {
	if len(rows) == 0 {
		return nil
	}

	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return err
	}
	if err := validateRows(cfg, rows); err != nil {
		return err
	}

	batchCount := (len(rows) + cfg.BatchSize - 1) / cfg.BatchSize
	workers := cfg.Workers
	if workers > batchCount {
		workers = batchCount
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan []Row)
	var once sync.Once
	var firstErr error
	recordErr := func(err error) {
		if err == nil {
			return
		}
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				query, args, err := buildUpsertQuery(cfg, batch)
				if err != nil {
					recordErr(err)
					return
				}
				if _, err := db.ExecContext(ctx, query, args...); err != nil {
					recordErr(fmt.Errorf("upsert batch failed: %w", err))
					return
				}
			}
		}()
	}

sendLoop:
	for start := 0; start < len(rows); start += cfg.BatchSize {
		end := start + cfg.BatchSize
		if end > len(rows) {
			end = len(rows)
		}

		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- rows[start:end]:
		}
	}

	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func BuildUpsertQuery(cfg Config, rows []Row) (string, []any, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return "", nil, err
	}
	return buildUpsertQuery(cfg, rows)
}

func buildUpsertQuery(cfg Config, rows []Row) (string, []any, error) {
	if len(rows) == 0 {
		return "", nil, errors.New("rows must not be empty")
	}
	if err := validateRows(cfg, rows); err != nil {
		return "", nil, err
	}

	var builder strings.Builder
	builder.WriteString("INSERT INTO ")
	builder.WriteString(cfg.Table)
	builder.WriteString(" (")
	builder.WriteString(strings.Join(cfg.InsertColumns, ", "))
	builder.WriteString(") VALUES ")

	args := make([]any, 0, len(rows)*len(cfg.InsertColumns))
	argIndex := 1
	for rowIndex, row := range rows {
		if rowIndex > 0 {
			builder.WriteString(", ")
		}
		builder.WriteByte('(')
		for colIndex := range cfg.InsertColumns {
			if colIndex > 0 {
				builder.WriteString(", ")
			}
			switch cfg.Dialect {
			case DialectPostgres:
				_, _ = fmt.Fprintf(&builder, "$%d", argIndex)
				argIndex++
			case DialectMySQL, DialectSQLite:
				builder.WriteByte('?')
			default:
				return "", nil, fmt.Errorf("unsupported dialect: %q", cfg.Dialect)
			}
			args = append(args, row[colIndex])
		}
		builder.WriteByte(')')
	}

	switch cfg.Dialect {
	case DialectPostgres, DialectSQLite:
		builder.WriteString(" ON CONFLICT (")
		builder.WriteString(strings.Join(cfg.ConflictColumns, ", "))
		builder.WriteString(") DO UPDATE SET ")
		builder.WriteString(excludedAssignments(cfg.UpdateColumns))
	case DialectMySQL:
		builder.WriteString(" ON DUPLICATE KEY UPDATE ")
		builder.WriteString(mysqlAssignments(cfg.UpdateColumns))
	default:
		return "", nil, fmt.Errorf("unsupported dialect: %q", cfg.Dialect)
	}

	return builder.String(), args, nil
}

func normalizeConfig(cfg Config) (Config, error) {
	switch cfg.Dialect {
	case DialectPostgres, DialectMySQL, DialectSQLite:
	default:
		return Config{}, fmt.Errorf("unsupported dialect: %q", cfg.Dialect)
	}
	if err := validateTableName(cfg.Table); err != nil {
		return Config{}, fmt.Errorf("table: %w", err)
	}
	if len(cfg.InsertColumns) == 0 {
		return Config{}, errors.New("insert columns must not be empty")
	}
	if len(cfg.ConflictColumns) == 0 {
		return Config{}, errors.New("conflict columns must not be empty")
	}
	if err := validateColumnList(cfg.InsertColumns); err != nil {
		return Config{}, fmt.Errorf("insert columns: %w", err)
	}
	if err := validateColumnList(cfg.ConflictColumns); err != nil {
		return Config{}, fmt.Errorf("conflict columns: %w", err)
	}
	if err := ensureSubset(cfg.InsertColumns, cfg.ConflictColumns); err != nil {
		return Config{}, fmt.Errorf("conflict columns: %w", err)
	}

	if len(cfg.UpdateColumns) == 0 {
		cfg.UpdateColumns = defaultUpdateColumns(cfg.InsertColumns, cfg.ConflictColumns)
	}
	if len(cfg.UpdateColumns) == 0 {
		return Config{}, errors.New("update columns must not be empty")
	}
	if err := validateColumnList(cfg.UpdateColumns); err != nil {
		return Config{}, fmt.Errorf("update columns: %w", err)
	}
	if err := ensureSubset(cfg.InsertColumns, cfg.UpdateColumns); err != nil {
		return Config{}, fmt.Errorf("update columns: %w", err)
	}

	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 250
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers()
	}

	return cfg, nil
}

func validateRows(cfg Config, rows []Row) error {
	for i, row := range rows {
		if len(row) != len(cfg.InsertColumns) {
			return fmt.Errorf(
				"row %d has %d values; expected %d values to match insert columns",
				i,
				len(row),
				len(cfg.InsertColumns),
			)
		}
	}
	return nil
}

func defaultUpdateColumns(insertColumns, conflictColumns []string) []string {
	conflictSet := make(map[string]struct{}, len(conflictColumns))
	for _, column := range conflictColumns {
		conflictSet[column] = struct{}{}
	}

	updateColumns := make([]string, 0, len(insertColumns))
	for _, column := range insertColumns {
		if _, exists := conflictSet[column]; exists {
			continue
		}
		updateColumns = append(updateColumns, column)
	}
	return updateColumns
}

func ensureSubset(insertColumns, targetColumns []string) error {
	insertSet := make(map[string]struct{}, len(insertColumns))
	for _, column := range insertColumns {
		insertSet[column] = struct{}{}
	}

	for _, column := range targetColumns {
		if _, exists := insertSet[column]; !exists {
			return fmt.Errorf("%q is not present in insert columns", column)
		}
	}
	return nil
}

func validateTableName(name string) error {
	if name == "" {
		return errors.New("must not be empty")
	}

	parts := strings.Split(name, ".")
	for _, part := range parts {
		if err := validateBareIdentifier(part); err != nil {
			return err
		}
	}
	return nil
}

func validateColumnList(columns []string) error {
	for _, column := range columns {
		if err := validateBareIdentifier(column); err != nil {
			return err
		}
	}
	return nil
}

func validateBareIdentifier(name string) error {
	if name == "" {
		return errors.New("must not be empty")
	}

	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return fmt.Errorf("invalid identifier %q", name)
		}
	}
	return nil
}

func excludedAssignments(columns []string) string {
	assignments := make([]string, 0, len(columns))
	for _, column := range columns {
		assignments = append(assignments, column+" = excluded."+column)
	}
	return strings.Join(assignments, ", ")
}

func mysqlAssignments(columns []string) string {
	assignments := make([]string, 0, len(columns))
	for _, column := range columns {
		assignments = append(assignments, column+" = VALUES("+column+")")
	}
	return strings.Join(assignments, ", ")
}

func defaultWorkers() int {
	workers := runtime.GOMAXPROCS(0)
	switch {
	case workers <= 0:
		return 1
	case workers > 4:
		return 4
	default:
		return workers
	}
}
