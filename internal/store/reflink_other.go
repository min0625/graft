// Copyright 2026 The Graft Authors

//go:build !linux

package store

import (
	"errors"
	"io/fs"
)

// errReflinkUnsupported drives the plain-copy fallback on platforms where
// graft does not implement copy-on-write cloning.
var errReflinkUnsupported = errors.New("reflink not supported on this platform")

// reflink always fails on non-Linux platforms so copyOrReflink falls back to a
// plain copy. ReFS and APFS clones are a future optimization (spec §5.4).
func reflink(_, _ string, _ fs.FileMode) error {
	return errReflinkUnsupported
}
