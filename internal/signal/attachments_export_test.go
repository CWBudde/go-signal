//go:build cgo && !purego

package signal

// CheckDownload exposes checkDownload to the signal_test package.
func CheckDownload(att Attachment) error {
	return checkDownload(att)
}

// DownloadError exposes downloadError to the signal_test package.
func DownloadError(err error) error {
	return downloadError(err)
}
