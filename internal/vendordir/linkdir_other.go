// Copyright 2026 The Graft Authors

//go:build !windows

package vendordir

import "os"

// linkDir creates a directory symlink at link pointing at target (spec §5.4).
// The target is absolute (a store entry), so the link resolves the same from
// any vendor location.
func linkDir(target, link string) error {
	return os.Symlink(target, link)
}
