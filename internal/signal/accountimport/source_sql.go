//go:build linux && (cgo || libsignal_go)

package accountimport

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"

	"github.com/cwbudde/go-signal/internal/signal"
	"golang.org/x/sys/unix"
)

const sourceDatabaseName = "account.db"

type sourceDB struct {
	db              *sql.Conn
	pool            *sql.DB
	closed          bool
	tables          map[string]sourceTable
	options         snapshotOptions
	config          AccountConfig
	manifestVersion uint64
	validated       bool
}

// openSource only addresses the snapshot-owned copy. In particular it never
// opens the source with immutable=1, which would hide committed WAL contents.
func (snap *snapshot) openSource(ctx context.Context) (_ *sourceDB, err error) {
	if snap.closed {
		return nil, importError("closed snapshot SQL", signal.ErrInvalidAccountImport)
	}

	err = ctx.Err()
	if err != nil {
		return nil, sourceSQLError(ctx, "open", err)
	}

	err = snap.checkSQLCopies(ctx)
	if err != nil {
		return nil, err
	}

	dsn := (&url.URL{Scheme: "file", Opaque: snap.sqlPath, RawQuery: "mode=rw&" + sourceDriverOptions}).String()

	pool, err := sql.Open(sourceDriver, dsn)
	if err != nil {
		return nil, sourceSQLError(ctx, "open", err)
	}

	source := &sourceDB{pool: pool, options: snap.options}
	pool.SetMaxOpenConns(1)

	defer func() {
		if err != nil {
			err = errors.Join(err, source.Close())
		}
	}()

	source.db, err = pool.Conn(ctx)
	if err != nil {
		return nil, sourceSQLError(ctx, "connection", err)
	}
	// Hold one initialized connection for the complete source-reader lifetime;
	// cancellation or pool retirement must never silently lose its pragmas.
	// Disable application-defined schema execution before inspecting any table.
	// Recovery and sidecar writes are confined to the private directory.
	_, err = source.db.ExecContext(ctx, "PRAGMA trusted_schema=OFF; PRAGMA query_only=ON")
	if err != nil {
		return nil, sourceSQLError(ctx, "open", err)
	}

	return source, nil
}

func (source *sourceDB) Close() error {
	if source.closed {
		return nil
	}

	source.closed = true
	source.validated = false

	var result error
	if source.db != nil {
		result = source.db.Close()
	}

	result = errors.Join(result, source.pool.Close())
	if result != nil {
		return importError("SQL close", signal.ErrInvalidAccountImport)
	}

	return nil
}

func sourceSQLError(ctx context.Context, category string, cause error) error {
	if cause == nil {
		return nil
	}

	err := ctx.Err()
	if err != nil {
		return importError("SQL "+category, err)
	}
	// Driver diagnostics can contain the failing value or schema text.
	return importError("SQL "+category, signal.ErrInvalidAccountImport)
}

func (source *sourceDB) validate(ctx context.Context, snap *snapshot) error {
	source.validated = false

	checks := []func(context.Context) error{
		source.validateSchema,
		func(ctx context.Context) error { return source.validatePending(ctx, snap) },
		source.validateLimits, source.validateIntegrity, source.readConfiguration,
		func(ctx context.Context) error { return source.validateRecords(ctx, snap) },
	}
	for _, validate := range checks {
		err := ctx.Err()
		if err != nil {
			return sourceSQLError(ctx, "validate", err)
		}

		err = validate(ctx)
		if err != nil {
			return err
		}
	}

	source.validated = true

	return nil
}

func (source *sourceDB) validatePending(ctx context.Context, snap *snapshot) error {
	for _, entry := range snap.cache {
		if entry.stat.Mode&unix.S_IFMT != unix.S_IFDIR {
			return importError("pending cache", signal.ErrUnsupportedAccountImport)
		}
	}

	for _, table := range []string{
		"message_send_log", "message_send_log_content", "group_v1", "group_v1_member",
	} {
		var present bool

		err := source.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+quoteSQLName(table)+" LIMIT 1)").Scan(&present)
		if err != nil {
			return sourceSQLError(ctx, "pending work", err)
		}

		if present {
			return importError("pending or unsupported rows", signal.ErrUnsupportedAccountImport)
		}
	}

	return nil
}

func sourceRecordTables() []string {
	return []string{
		"pre_key", "signed_pre_key", "kyber_pre_key", sourceIdentityTable, sourceSessionTable, sourceSenderKeyTable,
		sourceRecipientTable, sourceGroupTable,
	}
}

func (source *sourceDB) validateRecords(ctx context.Context, snap *snapshot) error {
	for _, table := range sourceRecordTables() {
		err := source.validateDuplicates(ctx, table)
		if err != nil {
			return err
		}

		err = source.each(ctx, table, func(row sourceRow) error { return validateSourceRow(row, snap) })
		if err != nil {
			return err
		}
	}

	return nil
}

func (source *sourceDB) validateIntegrity(ctx context.Context) error {
	good := false

	const query = "SELECT CASE WHEN integrity_check='ok' THEN 1 ELSE 0 END FROM pragma_integrity_check"

	err := sourceSchemaRows(ctx, source.db, query, nil, func(row *sql.Rows) error {
		var valid int

		scanErr := row.Scan(&valid)
		if scanErr != nil {
			return sourceSQLError(ctx, "integrity", scanErr)
		}

		if valid != 1 {
			return importError("SQL integrity", signal.ErrInvalidAccountImport)
		}

		good = true

		return nil
	})
	if err != nil {
		return err
	}

	if !good {
		return importError("SQL integrity", signal.ErrInvalidAccountImport)
	}

	var failures int64

	err = source.db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_foreign_key_check").Scan(&failures)
	if err != nil || failures != 0 {
		return sourceSQLError(ctx, "foreign keys", signal.ErrInvalidAccountImport)
	}

	return nil
}

func (source *sourceDB) validateLimits(ctx context.Context) error {
	var protocols int64

	for _, table := range sourceRecordTables() {
		var count int64

		err := source.db.QueryRowContext(ctx, "SELECT count(*) FROM "+quoteSQLName(table)).Scan(&count)
		if err != nil {
			return sourceSQLError(ctx, "row count", err)
		}

		limit := source.options.limits.protocolRows

		switch table {
		case sourceRecipientTable:
			limit = source.options.limits.recipientRows
		case sourceGroupTable:
			limit = source.options.limits.groupRows
		default:
			protocols += count
		}

		if count > limit || protocols > source.options.limits.protocolRows {
			return importError("SQL row bound", signal.ErrInvalidAccountImport)
		}
	}

	return nil
}

// Check the rooted workspace and fixed SQL names immediately before handing a
// path to SQLite. A replaced directory, symlink or source hard link is not a
// private copy, even if its bytes happen to match.
func (snap *snapshot) checkSQLCopies(ctx context.Context) error {
	err := snap.revalidatePaths(ctx)
	if err != nil {
		return err
	}

	bound, err := snap.tempParent.Lstat(snap.tempName)
	if err != nil {
		return importError("SQL workspace binding", signal.ErrAccountImportSourceChanged)
	}

	held, err := snap.tempRoot.Stat(".")
	if err != nil || !os.SameFile(bound, held) {
		return importError("SQL workspace binding", signal.ErrAccountImportSourceChanged)
	}

	for _, name := range []string{sourceDatabaseName, "account.db-wal", "account.db-shm"} {
		err = snap.checkSQLCopy(name)
		if err != nil {
			return err
		}
	}

	return nil
}

func (snap *snapshot) checkSQLCopy(name string) error {
	info, err := snap.tempRoot.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) && name != sourceDatabaseName {
		return nil
	}

	if err != nil || !info.Mode().IsRegular() {
		return importError("SQL copy binding", signal.ErrAccountImportSourceChanged)
	}

	for _, original := range snap.source.files {
		pinned, statErr := original.file.Stat()
		if statErr != nil || os.SameFile(info, pinned) {
			return importError("SQL copy alias", signal.ErrAccountImportSourceChanged)
		}
	}

	return nil
}
