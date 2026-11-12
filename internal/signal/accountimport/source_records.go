//go:build linux && (cgo || libsignal_go)

package accountimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

const (
	sourceSessionTable   = "session"
	sourceSenderKeyTable = "sender_key"
	sourceACIColumn      = "aci"
	sourcePNIColumn      = "pni"

	sourceSQLNull          = "null"
	sourceSQLInteger       = "integer"
	sourceSQLText          = "text"
	sourceSQLBlob          = "blob"
	sourceIdentityTable    = "identity"
	sourceRecipientTable   = "recipient"
	sourceGroupTable       = "group_v2"
	sourceSharingEverybody = "EVERYBODY"
	sourceSharingNobody    = "NOBODY"
)

// sourceValue preserves SQL NULL independently of zero/false and empty strings.
// Every value has passed the declared-type and byte-bound gate before use.
type (
	sourceValue struct {
		kind  string
		value any
	}
	sourceServiceID struct {
		pni bool
		id  uuid.UUID
	}
	sourceRow struct {
		table  string
		values map[string]sourceValue
		ids    map[string]sourceServiceID
	}
)

func (row sourceRow) integer(name string) int64 {
	value, _ := row.values[name].value.(int64)
	return value
}

func (row sourceRow) text(name string) string {
	value, _ := row.values[name].value.(string)
	return value
}

func (row sourceRow) blob(name string) []byte {
	value, _ := row.values[name].value.([]byte)
	return value
}
func (row sourceRow) null(name string) bool { return row.values[name].kind == sourceSQLNull }

// each streams one bounded row. Names come exclusively from the validated
// compiled contract, never from a source value or a caller's SQL expression.
func (source *sourceDB) each(ctx context.Context, table string, visit func(sourceRow) error) (err error) {
	schema, ok := source.tables[table]
	if !ok {
		return importError("SQL unknown table", signal.ErrInvalidAccountImport)
	}

	var expressions []string

	for _, column := range schema.columns {
		name := quoteSQLName(column.name)

		const projection = "CASE WHEN length(CAST(%s AS BLOB))<=%d THEN %s ELSE NULL END," +
			"typeof(%s),length(CAST(%s AS BLOB))"

		expressions = append(expressions, fmt.Sprintf(projection, name, source.valueLimit(table), name, name, name))
	}

	query := "SELECT " + strings.Join(expressions, ",") + " FROM " + quoteSQLName(table) + " ORDER BY _id"

	rows, err := source.db.QueryContext(ctx, query)
	if err != nil {
		return sourceSQLError(ctx, "rows", err)
	}
	defer func() { err = errors.Join(err, sourceSQLError(ctx, "rows close", rows.Close())) }()

	for rows.Next() {
		row, rowErr := readSourceRow(rows, table, schema, source.valueLimit(table))
		if rowErr != nil {
			return sourceSQLError(ctx, "row", rowErr)
		}

		source.options.notify("sql:row:" + table)

		err = ctx.Err()
		if err != nil {
			return sourceSQLError(ctx, "row", err)
		}

		err = visit(row)
		if err != nil {
			return err
		}
	}

	return sourceSQLError(ctx, "rows", rows.Err())
}

const sourceCellWidth = 3

func readSourceRow(rows *sql.Rows, table string, schema sourceTable, limit int64) (sourceRow, error) {
	row := sourceRow{table: table, values: make(map[string]sourceValue), ids: make(map[string]sourceServiceID)}
	raw := make([]any, len(schema.columns)*sourceCellWidth)

	targets := make([]any, len(raw))
	for i := range raw {
		targets[i] = &raw[i]
	}

	err := rows.Scan(targets...)
	if err != nil {
		return row, signal.ErrInvalidAccountImport
	}

	for i, column := range schema.columns {
		offset := i * sourceCellWidth

		value, cellErr := sourceCell(column, raw[offset:offset+sourceCellWidth], limit)
		if cellErr != nil {
			return row, cellErr
		}

		row.values[column.name] = value
	}

	err = decodeSourceIDs(&row)

	return row, err
}

func (source *sourceDB) valueLimit(table string) int64 {
	if isProtocolTable(table) {
		return source.options.limits.blobBytes
	}

	return snapshotJSONBytes
}

func sourceCell(column sourceColumn, raw []any, limit int64) (sourceValue, error) {
	kind, ok := raw[1].(string)
	if !ok {
		return sourceValue{}, signal.ErrInvalidAccountImport
	}

	value := sourceValue{kind: kind, value: raw[0]}
	if kind == sourceSQLNull {
		if column.notNull != 0 || column.primaryKey != 0 {
			return value, signal.ErrInvalidAccountImport
		}

		return value, nil
	}

	length, ok := raw[2].(int64)
	if !ok || length > limit || raw[0] == nil {
		return value, signal.ErrInvalidAccountImport
	}

	wanted := map[string]string{
		"INTEGER": sourceSQLInteger, "TEXT": sourceSQLText, "BLOB": sourceSQLBlob, "ANY": kind,
	}[column.kind]
	if wanted != kind {
		return value, signal.ErrInvalidAccountImport
	}

	return value, nil
}

func isProtocolTable(table string) bool {
	switch table {
	case "pre_key", "signed_pre_key", "kyber_pre_key", sourceIdentityTable, sourceSessionTable, sourceSenderKeyTable:
		return true
	default:
		return false
	}
}

func (source *sourceDB) validateDuplicates(ctx context.Context, table string) error {
	var keys []string

	switch table {
	case sourceIdentityTable:
		keys = []string{"lower(address)"}
	case sourceSessionTable:
		keys = []string{"account_id_type,lower(address),device_id"}
	case sourceSenderKeyTable:
		keys = []string{"lower(address),device_id,distribution_id"}
	case sourceRecipientTable:
		keys = []string{"lower(aci)", "lower(pni)", "number", "username"}
	default:
		return nil
	}

	for _, key := range keys {
		where := ""
		if table == sourceRecipientTable {
			where = " WHERE " + key + " IS NOT NULL"
		}

		var duplicate bool

		query := "SELECT EXISTS(SELECT 1 FROM " + quoteSQLName(table) + where +
			" GROUP BY " + key + " HAVING count(*)>1 LIMIT 1)"

		err := source.db.QueryRowContext(ctx, query).Scan(&duplicate)
		if err != nil {
			return sourceSQLError(ctx, "duplicates", err)
		}

		if duplicate {
			return importError("SQL duplicate address", signal.ErrInvalidAccountImport)
		}
	}

	return nil
}

func parseSourceServiceID(value string) (sourceServiceID, error) {
	pni := strings.HasPrefix(value, "PNI:")
	bare := strings.TrimPrefix(value, "PNI:")

	id, err := uuid.Parse(bare)
	if err != nil || id == uuid.Nil || len(bare) != 36 || !strings.EqualFold(id.String(), bare) {
		return sourceServiceID{}, signal.ErrInvalidAccountImport
	}

	return sourceServiceID{pni: pni, id: id}, nil
}

func validateSourceRow(row sourceRow, snap *snapshot) error {
	if isProtocolTable(row.table) {
		return validateProtocolRow(row)
	}

	switch row.table {
	case sourceRecipientTable:
		return validateRecipientRow(row, snap)
	case sourceGroupTable:
		return validateGroupRow(row)
	default:
		return importError("SQL internal table", signal.ErrInvalidAccountImport)
	}
}

func validateProtocolRow(row sourceRow) error {
	if _, exists := row.values["account_id_type"]; exists {
		namespace := row.integer("account_id_type")
		if namespace < 0 || namespace > 1 {
			return importError("SQL protocol namespace", signal.ErrInvalidAccountImport)
		}
	}

	err := validateProtocolNumbers(row)
	if err != nil {
		return err
	}

	for _, column := range []string{"record", "serialized", "identity_key", "public_key", "private_key", "signature"} {
		if _, exists := row.values[column]; exists && len(row.blob(column)) == 0 {
			return importError("SQL empty protocol blob", signal.ErrInvalidAccountImport)
		}
	}

	if _, exists := row.values["distribution_id"]; exists && !validSourceUUID(row.blob("distribution_id")) {
		return importError("SQL distribution ID", signal.ErrInvalidAccountImport)
	}

	return nil
}

func validateProtocolNumbers(row sourceRow) error {
	for _, column := range []string{"key_id", "device_id", "is_last_resort", "trust_level"} {
		if _, exists := row.values[column]; !exists {
			continue
		}

		value := row.integer(column)
		maximum := int64(math.MaxUint32)
		minimum := int64(0)

		switch column {
		case "key_id":
			maximum = 0xfffffe
		case "device_id":
			minimum = 1
		case "is_last_resort":
			maximum = 1
		case "trust_level":
			maximum = 2
		}

		if value < minimum || value > maximum {
			return importError("SQL protocol number", signal.ErrInvalidAccountImport)
		}
	}

	return nil
}

func decodeSourceIDs(row *sourceRow) error {
	for _, name := range []string{"address", sourceACIColumn, sourcePNIColumn} {
		if _, exists := row.values[name]; !exists || row.null(name) {
			continue
		}

		id, err := parseSourceServiceID(row.text(name))
		if err != nil || (name == sourceACIColumn && id.pni) || (name == sourcePNIColumn && !id.pni) {
			return importError("SQL typed address", signal.ErrInvalidAccountImport)
		}

		row.ids[name] = id
	}

	return nil
}

// rows is the conversion-facing reader: only a fully validated source can emit
// rows, and every pass decodes typed IDs rather than relying on a previous pass.
func (source *sourceDB) rows(ctx context.Context, table string, visit func(sourceRow) error) error {
	if !source.validated {
		return importError("SQL not validated", signal.ErrInvalidAccountImport)
	}

	return source.each(ctx, table, visit)
}

func validSourceUUID(data []byte) bool {
	return len(data) == sourceUUIDBytes && uuid.UUID(data) != uuid.Nil
}
