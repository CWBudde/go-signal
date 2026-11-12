//go:build linux && (cgo || libsignal_go)

package accountimport

import (
	"context"
	"errors"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// SnapshotTestOptions lowers bounds and installs per-instance deterministic barriers.
type SnapshotTestOptions struct {
	JSONLimit, SQLLimit                                                        int64
	EntryLimit                                                                 int
	ProtocolRowLimit, RecipientRowLimit, GroupRowLimit, BlobLimit, SchemaLimit int64
	TempDir                                                                    string
	Step                                                                       func(string)
	MountInfo                                                                  func() ([]byte, error)
}

// CheckRequiredFileForTest exercises the no-follow regular-file gate on real devices.
func CheckRequiredFileForTest(ctx context.Context, parent, name string) error {
	var tree sourceTree

	directory, err := tree.absoluteDirectory(ctx, parent)
	if err == nil {
		_, err = tree.regularFile(directory, name, "test required file", false)
	}

	return errors.Join(err, tree.close())
}

// SnapshotForTest exposes snapshot observations without adding a production API.
type SnapshotForTest struct {
	*snapshot

	Workspace, Database, Number, ACI, PNI, Destination string
}

// OpenSnapshotForTest exercises the real snapshot reader with instance-local hooks.
func OpenSnapshotForTest(ctx context.Context, opts signal.AccountImportOptions,
	config SnapshotTestOptions,
) (*SnapshotForTest, error) {
	limits := config.snapshotLimits()

	var (
		snap *snapshot
		err  error
	)

	if config.isDefault() {
		snap, err = openSnapshot(ctx, opts)
	} else {
		snap, err = openSnapshotWithOptions(ctx, opts, snapshotOptions{
			limits: limits, tempDir: config.TempDir, step: config.Step, mountInfo: config.MountInfo,
		})
	}

	if err != nil {
		return nil, err
	}

	return &SnapshotForTest{
		snapshot: snap, Workspace: snap.workspace, Database: snap.sqlPath,
		Number: *snap.account.Number, ACI: *snap.account.ACI.ServiceID,
		PNI: strings.TrimPrefix(*snap.account.PNI.ServiceID, "PNI:"), Destination: snap.destination.resolved,
	}, nil
}

func (config SnapshotTestOptions) snapshotLimits() snapshotLimits {
	limits := defaultSnapshotLimits()

	overrides := []struct {
		target *int64
		value  int64
	}{
		{&limits.jsonBytes, config.JSONLimit},
		{&limits.sqlBytes, config.SQLLimit},
		{&limits.protocolRows, config.ProtocolRowLimit},
		{&limits.recipientRows, config.RecipientRowLimit},
		{&limits.groupRows, config.GroupRowLimit},
		{&limits.blobBytes, config.BlobLimit},
		{&limits.schemaBytes, config.SchemaLimit},
	}
	for _, override := range overrides {
		if override.value > 0 {
			*override.target = override.value
		}
	}

	if config.EntryLimit > 0 {
		limits.pathEntries = config.EntryLimit
	}

	return limits
}

func (config SnapshotTestOptions) isDefault() bool {
	return config.TempDir == "" && config.Step == nil && config.MountInfo == nil &&
		config.snapshotLimits() == defaultSnapshotLimits()
}

// ValidateSourceForTest opens and validates only the private SQLite copy.
func (snap *SnapshotForTest) ValidateSourceForTest(ctx context.Context) error {
	source, err := snap.openSource(ctx)
	if err != nil {
		return err
	}

	return errors.Join(source.validate(ctx, snap.snapshot), source.Close())
}

// SourceConfigForTest observes preserved SQL NULL and false configuration values.
func (snap *SnapshotForTest) SourceConfigForTest(ctx context.Context) (_ AccountConfig, err error) {
	source, err := snap.openSource(ctx)
	if err != nil {
		return AccountConfig{}, err
	}
	defer func() { err = errors.Join(err, source.Close()) }()

	err = source.validate(ctx, snap.snapshot)

	return source.config, err
}

// SourceRowForTest observes values and decoded namespace-aware IDs.
type SourceRowForTest struct {
	Values     map[string]any
	ServiceIDs map[string]string
}

// SourceRowsForTest collects rows only in tests; production readers stream rows.
func (snap *SnapshotForTest) SourceRowsForTest(ctx context.Context, table string) (_ []SourceRowForTest, err error) {
	source, err := snap.openSource(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, source.Close()) }()

	err = source.validate(ctx, snap.snapshot)
	if err != nil {
		return nil, err
	}

	var result []SourceRowForTest

	err = source.rows(ctx, table, func(row sourceRow) error {
		values := make(map[string]any, len(row.values))
		for name, cell := range row.values {
			values[name] = cell.value
		}

		ids := make(map[string]string, len(row.ids))
		for name, id := range row.ids {
			value := id.id.String()
			if id.pni {
				value = "PNI:" + value
			}

			ids[name] = value
		}

		result = append(result, SourceRowForTest{Values: values, ServiceIDs: ids})

		return nil
	})

	return result, err
}

// SourceConnectionForTest forces idle-pool retirement to verify that validation
// owns the initialized connection rather than borrowing an arbitrary pool handle.
//
//nolint:nonamedreturns // Named results let deferred connection cleanup join failures.
func (snap *SnapshotForTest) SourceConnectionForTest(ctx context.Context,
) (trusted, queryOnly int, writeErr, err error) {
	source, err := snap.openSource(ctx)
	if err != nil {
		return 0, 0, nil, sourceSQLError(ctx, "test connection", err)
	}
	defer func() { err = errors.Join(err, source.Close()) }()

	source.pool.SetMaxIdleConns(0)

	err = source.db.QueryRowContext(ctx, "PRAGMA trusted_schema").Scan(&trusted)
	if err != nil {
		return 0, 0, nil, sourceSQLError(ctx, "test connection", err)
	}

	err = source.db.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly)
	if err != nil {
		return 0, 0, nil, sourceSQLError(ctx, "test connection", err)
	}

	_, writeErr = source.db.ExecContext(ctx, "DELETE FROM key_value")

	return trusted, queryOnly, sourceSQLError(ctx, "test write", writeErr), nil
}
