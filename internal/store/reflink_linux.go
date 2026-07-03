// Copyright 2026 The Graft Authors

//go:build linux

package store

import (
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// reflink creates dst as a copy-on-write clone of src via the FICLONE ioctl,
// supported on btrfs, XFS, and other CoW filesystems (spec §5.4). It returns
// an error — leaving no partial dst — when the filesystem does not support
// cloning, so the caller falls back to a plain copy.
func reflink(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // The path comes from the store tree.
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // Read-only file.

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec // Same as above.
	if err != nil {
		return err
	}

	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		out.Close()    //nolint:errcheck,gosec // The clone error wins.
		os.Remove(dst) //nolint:errcheck,gosec // Best-effort cleanup of the empty file.

		return err
	}

	return out.Close()
}
