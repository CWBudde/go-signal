package signal

import "errors"

// ErrInvalidAccountImport means that required import input is missing or malformed.
var ErrInvalidAccountImport = errors.New("invalid account import")

// ErrUnsupportedAccountImport means that the source or platform is outside the supported subset.
var ErrUnsupportedAccountImport = errors.New("unsupported account import")

// ErrAmbiguousAccountImport means that the source selector does not identify exactly one account.
var ErrAmbiguousAccountImport = errors.New("ambiguous account import")

// ErrAccountImportSourceChanged means that the stopped source changed during import.
var ErrAccountImportSourceChanged = errors.New("account import source changed")

// AccountImportOptions selects a stopped signal-cli snapshot and a destination data directory.
type AccountImportOptions struct {
	SourceDir     string
	SourceAccount string
	DataDir       string
}

// AccountImportFormat records the validated source format versions.
type AccountImportFormat struct {
	Registry    int
	AccountJSON int
	SQLite      int
}

// AccountImportCounts counts retained source records across both local service namespaces.
// Identities counts source rows, not target trust copies. KyberPreKeys includes
// the LastResortKyberPreKeys subset. Groups counts group-v2 master keys.
type AccountImportCounts struct {
	Identities             uint64
	Sessions               uint64
	PreKeys                uint64
	SignedPreKeys          uint64
	KyberPreKeys           uint64
	LastResortKyberPreKeys uint64
	SenderKeys             uint64
	Recipients             uint64
	Groups                 uint64
	ChatTimers             uint64
}

// AccountImportResult describes local publication, not server validity or live delivery.
// Unavailable device names and link/sync times remain unknown in Account. Excluded
// contains fixed category names, never source paths, credentials or record bytes.
type AccountImportResult struct {
	Account      Account
	Counts       AccountImportCounts
	Excluded     []string
	SourceFormat AccountImportFormat
}
