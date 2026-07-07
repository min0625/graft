// Copyright 2026 The Graft Authors

//go:build windows

package vendordir

import (
	"encoding/binary"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// linkDir creates a directory junction at link pointing at target (spec §5.4).
// A junction — not a symlink — is used because it needs no administrator
// privilege. The reparse point is set via DeviceIoControl (FSCTL_SET_REPARSE_POINT)
// to avoid spawning cmd.exe. target must be an absolute path.
func linkDir(target, link string) error {
	if err := os.Mkdir(link, 0o755); err != nil { //nolint:gosec // Vendor trees are world-readable by design.
		return fmt.Errorf("create junction dir: %w", err)
	}

	if err := setJunction(link, target); err != nil {
		os.Remove(link) //nolint:errcheck,gosec // Best-effort cleanup; setJunction error wins.
		return fmt.Errorf("set junction: %w", err)
	}

	return nil
}

func setJunction(link, target string) error {
	// Junction substitute name must use the NT path prefix.
	sub := windows.StringToUTF16(`\??\` + target)
	sub = sub[:len(sub)-1] // strip null terminator; byte lengths below exclude it

	// REPARSE_DATA_BUFFER wire layout for IO_REPARSE_TAG_MOUNT_POINT:
	//   [0:4]   ReparseTag           = IO_REPARSE_TAG_MOUNT_POINT
	//   [4:6]   ReparseDataLength    = 8 + len(PathBuffer in bytes)
	//   [6:8]   Reserved             = 0
	//   [8:10]  SubstituteNameOffset = 0
	//   [10:12] SubstituteNameLength = len(sub) * 2
	//   [12:14] PrintNameOffset      = len(sub)*2 + 2  (past sub + NUL)
	//   [14:16] PrintNameLength      = 0
	//   [16:]   PathBuffer: sub (UTF-16LE) + NUL + NUL (empty print name)
	// bufOverhead is the 16-byte header plus the NUL and empty print-name
	// terminators (2+2 bytes) added around subBytes when building pathBufLen
	// and buf further down — named so the bound check can't silently drift
	// from that construction.
	const bufOverhead = 16 + 2 + 2

	// windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE (16 KiB) is the kernel's real
	// cap on the whole buffer (header + PathBuffer); it's far tighter than
	// the uint16 fields below could otherwise hold, so bounding subBytes
	// against it also keeps every conversion in range.
	subBytes := len(sub) * 2
	if subBytes > windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE-bufOverhead {
		return fmt.Errorf("target path too long for a junction: %d bytes", subBytes)
	}

	linkPtr, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}

	h, err := windows.CreateFile(
		linkPtr,
		windows.GENERIC_WRITE,
		0, nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return fmt.Errorf("open for reparse: %w", err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck

	pathBufLen := subBytes + 2 + 2
	buf := make([]byte, 16+pathBufLen)

	binary.LittleEndian.PutUint32(buf[0:], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(8+pathBufLen))
	binary.LittleEndian.PutUint16(buf[10:], uint16(subBytes))
	binary.LittleEndian.PutUint16(buf[12:], uint16(subBytes+2))

	for i, c := range sub {
		binary.LittleEndian.PutUint16(buf[16+i*2:], c)
	}

	var n uint32

	return windows.DeviceIoControl(
		h, windows.FSCTL_SET_REPARSE_POINT,
		&buf[0], uint32(len(buf)), //nolint:gosec // buf length is bounded by the pathBufLen check above
		nil, 0, &n, nil,
	)
}
