//go:build linux && (cgo || libsignal_go)

package accountimport

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// sourceSchema is trusted DDL. Pragmas compare columns, defaults, keys, indexes
// and strictness independently of whitespace and object creation order.
//
//go:embed source_schema.sql
var sourceSchema string

const sourceSQLiteVersion = 31

type sourceColumn struct {
	name, kind                  string
	notNull, primaryKey, hidden int
	defaultValue                sql.NullString
}

type sourceTable struct {
	columns                             []sourceColumn
	foreignKeys, indexes                []string
	strict, withoutRowID, autoIncrement int
}

// The pinned source connection and trusted contract database both implement this.
type sourceSQLReader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func quoteSQLName(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func (source *sourceDB) validateSchema(ctx context.Context) (err error) {
	var version int

	err = source.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	if err != nil {
		return sourceSQLError(ctx, "schema version", err)
	}

	if version != sourceSQLiteVersion {
		return importError("SQL version", signal.ErrUnsupportedAccountImport)
	}

	expected, err := sql.Open(sourceDriver, ":memory:")
	if err != nil {
		return sourceSQLError(ctx, "schema contract", err)
	}

	defer func() { err = errors.Join(err, sourceSQLError(ctx, "schema close", expected.Close())) }()

	expected.SetMaxOpenConns(1)

	_, err = expected.ExecContext(ctx, sourceSchema)
	if err != nil {
		return sourceSQLError(ctx, "schema contract", err)
	}

	wanted, err := inspectSourceSchema(ctx, expected, source.options.limits.schemaBytes)
	if err != nil {
		return err
	}

	actual, err := inspectSourceSchema(ctx, source.db, source.options.limits.schemaBytes)
	if err != nil {
		return err
	}

	if !reflect.DeepEqual(actual, wanted) {
		return importError("SQL schema contract", signal.ErrInvalidAccountImport)
	}

	source.tables = wanted

	return nil
}

// Inspect raw sqlite_schema before querying application tables. This first query
// returns integers only: no hostile SQL or identifiers are scanned unbounded.
func inspectSourceSchema(ctx context.Context, reader sourceSQLReader, limit int64) (map[string]sourceTable, error) {
	const query = `SELECT count(*),coalesce(max(length(CAST(sql AS BLOB))),0),
 coalesce(max(max(length(CAST(name AS BLOB)),length(CAST(tbl_name AS BLOB)))),0) FROM sqlite_schema`

	var count, sqlBytes, nameBytes int64

	err := reader.QueryRowContext(ctx, query).Scan(&count, &sqlBytes, &nameBytes)
	if err != nil {
		return nil, sourceSQLError(ctx, "schema bounds", err)
	}

	if count > 128 || sqlBytes > limit || nameBytes > 128 {
		return nil, importError("SQL schema bound", signal.ErrInvalidAccountImport)
	}

	tables, err := sourceSchemaObjects(ctx, reader)
	if err != nil {
		return nil, err
	}

	for name, table := range tables {
		table, err = inspectSourceTable(ctx, reader, name, table)
		if err != nil {
			return nil, err
		}

		tables[name] = table
	}

	return tables, nil
}

func sourceSchemaObjects(ctx context.Context, reader sourceSQLReader) (map[string]sourceTable, error) {
	tables := make(map[string]sourceTable)

	const query = "SELECT type,name,sql FROM sqlite_schema ORDER BY name"

	err := sourceSchemaRows(ctx, reader, query, nil, func(row *sql.Rows) error {
		var (
			kind, name string
			statement  sql.NullString
		)

		scanErr := row.Scan(&kind, &name, &statement)
		if scanErr != nil {
			return sourceSQLError(ctx, "schema object", scanErr)
		}

		if kind != "table" && kind != "index" {
			return importError("SQL executable schema", signal.ErrInvalidAccountImport)
		}

		if statement.Valid && forbiddenSourceSQL(statement.String) {
			return importError("SQL schema constraint", signal.ErrInvalidAccountImport)
		}

		if kind == "table" {
			table := sourceTable{}

			sqlTokens := strings.ToUpper(sourceSQLQuoted.ReplaceAllString(statement.String, " "))
			if strings.Contains(sqlTokens, "AUTOINCREMENT") {
				table.autoIncrement = 1
			}

			tables[name] = table
		}

		return nil
	})

	return tables, err
}

// Schema 31 has none of these extended executable/constraint forms. Pragmas
// inspect the remaining declarations, including all keys and index properties.
var (
	sourceForbiddenKeyword = regexp.MustCompile(`\b(CHECK|COLLATE|GENERATED|DEFERRABLE|VIRTUAL|WITHOUT|CONFLICT|AS)\b`)
	sourceSQLQuoted        = regexp.MustCompile(`(?s)'(?:''|[^'])*'|"(?:""|[^"])*"|` +
		"`(?:``|[^`])*`" + `|\[[^\]]*\]|--[^\n]*(?:\n|$)|/\*.*?\*/`)
)

func forbiddenSourceSQL(statement string) bool {
	return sourceForbiddenKeyword.MatchString(strings.ToUpper(sourceSQLQuoted.ReplaceAllString(statement, " ")))
}

func inspectSourceTable(ctx context.Context, reader sourceSQLReader, name string, table sourceTable,
) (sourceTable, error) {
	err := reader.QueryRowContext(ctx,
		`SELECT wr,strict FROM pragma_table_list WHERE schema='main' AND name=?`, name,
	).Scan(&table.withoutRowID, &table.strict)
	if err != nil {
		return table, sourceSQLError(ctx, "schema table", err)
	}

	const columnQuery = `SELECT name,type,"notnull",dflt_value,pk,hidden FROM pragma_table_xinfo(?) ORDER BY cid`

	err = sourceSchemaRows(ctx, reader, columnQuery, []any{name}, func(row *sql.Rows) error {
		var column sourceColumn

		scanErr := row.Scan(&column.name, &column.kind, &column.notNull,
			&column.defaultValue, &column.primaryKey, &column.hidden)
		if scanErr != nil {
			return sourceSQLError(ctx, "schema column", scanErr)
		}

		column.kind = strings.ToUpper(column.kind)
		column.defaultValue = normalizeSourceDefault(column.defaultValue)
		table.columns = append(table.columns, column)

		return nil
	})
	if err != nil {
		return table, err
	}

	const foreignQuery = `SELECT "table","from","to",on_update,on_delete,match FROM pragma_foreign_key_list(?)`

	table.foreignKeys, err = sourceMetadata(ctx, reader, foreignQuery, name)
	if err != nil {
		return table, err
	}

	table.indexes, err = sourceIndexes(ctx, reader, name)

	return table, err
}

func normalizeSourceDefault(value sql.NullString) sql.NullString {
	if value.Valid {
		value.String = strings.ToUpper(strings.Trim(strings.TrimSpace(value.String), "()"))
		switch value.String {
		case "FALSE":
			value.String = "0"
		case "TRUE":
			value.String = "1"
		}
	}

	return value
}

func sourceMetadata(ctx context.Context, reader sourceSQLReader, query, name string) ([]string, error) {
	var result []string

	err := sourceSchemaRows(ctx, reader, query, []any{name}, func(row *sql.Rows) error {
		columns, columnErr := row.Columns()
		if columnErr != nil {
			return sourceSQLError(ctx, "schema metadata", columnErr)
		}

		values := make([]any, len(columns))

		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}

		scanErr := row.Scan(targets...)
		if scanErr != nil {
			return sourceSQLError(ctx, "schema metadata", scanErr)
		}

		result = append(result, fmt.Sprintf("%#v", values))

		return nil
	})

	slices.Sort(result)

	return result, err
}

func sourceIndexes(ctx context.Context, reader sourceSQLReader, table string) ([]string, error) {
	type sourceIndex struct {
		name, origin    string
		unique, partial int
	}

	var indexes []sourceIndex

	const indexQuery = `SELECT name,"unique",origin,partial FROM pragma_index_list(?)`

	err := sourceSchemaRows(ctx, reader, indexQuery, []any{table}, func(row *sql.Rows) error {
		var index sourceIndex

		scanErr := row.Scan(&index.name, &index.unique, &index.origin, &index.partial)
		if scanErr != nil {
			return sourceSQLError(ctx, "schema index", scanErr)
		}

		indexes = append(indexes, index)

		return nil
	})
	if err != nil {
		return nil, err
	}

	var result []string

	const fieldQuery = `SELECT seqno,cid,name,"desc",coll,"key" FROM pragma_index_xinfo(?)`
	for _, index := range indexes {
		fields, fieldErr := sourceMetadata(ctx, reader, fieldQuery, index.name)
		if fieldErr != nil {
			return nil, fieldErr
		}

		result = append(result, index.origin+strconv.Itoa(index.unique)+strconv.Itoa(index.partial)+strings.Join(fields, ";"))
	}

	slices.Sort(result)

	return result, nil
}

func sourceSchemaRows(ctx context.Context, reader sourceSQLReader, query string, args []any,
	visit func(*sql.Rows) error,
) (err error) {
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return sourceSQLError(ctx, "schema rows", err)
	}
	defer func() { err = errors.Join(err, sourceSQLError(ctx, "schema close", rows.Close())) }()

	for rows.Next() {
		err = visit(rows)
		if err != nil {
			return err
		}
	}

	return sourceSQLError(ctx, "schema rows", rows.Err())
}
