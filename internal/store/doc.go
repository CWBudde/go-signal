// Package store owns the on-disk data directory and the SQLite database that holds signalmeow's
// state. It needs cgo (SQLite and libsignal), so CGO_ENABLED=0 builds see only this file.
package store
