//go:build !purego

package signal

// LibsignalGoVersion returns "": this build calls libsignal itself, not libsignal-go.
func LibsignalGoVersion() string {
	return ""
}
