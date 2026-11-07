//go:build linux && (cgo || libsignal_go)

package accountimport

const (
	snapshotJSONBytes      = 4 << 20
	snapshotSQLBytes       = 4 << 30
	snapshotDirectoryMode  = 0o700
	snapshotFileMode       = 0o600
	snapshotCopyBuffer     = 64 << 10
	snapshotDirectoryBatch = 128
	snapshotDirectoryDepth = 64
	snapshotPathEntries    = 100_000
)

type snapshotLimits struct {
	jsonBytes, sqlBytes int64
	pathEntries         int
}

func defaultSnapshotLimits() snapshotLimits {
	return snapshotLimits{jsonBytes: snapshotJSONBytes, sqlBytes: snapshotSQLBytes, pathEntries: snapshotPathEntries}
}

type snapshotOptions struct {
	limits    snapshotLimits
	tempDir   string
	step      func(string)
	mountInfo func() ([]byte, error)
}

func (opts snapshotOptions) notify(step string) {
	if opts.step != nil {
		opts.step(step)
	}
}
