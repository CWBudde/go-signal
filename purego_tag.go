//go:build purego

package main

// The pure-Go backend is selected with the libsignal_go tag. The purego tag also makes the
// standard library drop its AES assembly for a variable-time table implementation, even on CPUs
// with AES instructions (docs/constant-time-review.md, CT-02), so it must not be set.
var _ = tag_purego_was_renamed_to_libsignal_go_see_docs_dev_md
