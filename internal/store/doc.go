// Package store owns the on-disk data directory:
//
//	<data-dir>/accounts.json      registry: number <-> ACI <-> device ID (see Dir.Accounts)
//	<data-dir>/accounts.lock      flock held briefly around every registry use (withRegistry)
//	<data-dir>/<aci>/account.db   SQLite: signalmeow's state plus our own tables
//	<data-dir>/<aci>/lock         flock held while a process is connected as that account
//	<data-dir>/.imports/<token>/  private import attempts (Dir.BeginImport, recovery.go)
//	<data-dir>/.removed/<token>/  removed account directories until they are deleted
//
// Directories are created 0700 and files 0600; looser existing permissions are logged as a
// warning. The layout, registry and lock need no cgo; opening a database does (store.go).
package store
