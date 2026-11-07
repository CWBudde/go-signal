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
	JSONLimit, SQLLimit int64
	EntryLimit          int
	TempDir             string
	Step                func(string)
	MountInfo           func() ([]byte, error)
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
	limits := defaultSnapshotLimits()
	if config.JSONLimit > 0 {
		limits.jsonBytes = config.JSONLimit
	}

	if config.SQLLimit > 0 {
		limits.sqlBytes = config.SQLLimit
	}

	if config.EntryLimit > 0 {
		limits.pathEntries = config.EntryLimit
	}

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

func (config SnapshotTestOptions) isDefault() bool {
	return config.TempDir == "" && config.Step == nil && config.MountInfo == nil &&
		config.JSONLimit == 0 && config.SQLLimit == 0 &&
		config.EntryLimit == 0
}
