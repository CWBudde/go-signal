//go:build linux && (cgo || libsignal_go)

package accountimport

const (
	sourceRecipientRows = 100_000
	sourceGroupRows     = 10_000
	sourceBlobBytes     = 1 << 20
	sourceSchemaBytes   = 64 << 10

	sourceProtocolRows     = 1_000_000
	sourceUUIDBytes        = 16
	sourceProfileKeyBytes  = 32
	sourceGroupIDBytes     = 32
	sourceGroupKeyBytes    = 32
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
	jsonBytes, sqlBytes                                            int64
	pathEntries                                                    int
	protocolRows, recipientRows, groupRows, blobBytes, schemaBytes int64
}

func defaultSnapshotLimits() snapshotLimits {
	return snapshotLimits{
		jsonBytes: snapshotJSONBytes, sqlBytes: snapshotSQLBytes, pathEntries: snapshotPathEntries,
		protocolRows: sourceProtocolRows, recipientRows: sourceRecipientRows, groupRows: sourceGroupRows,
		blobBytes: sourceBlobBytes, schemaBytes: sourceSchemaBytes,
	}
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
