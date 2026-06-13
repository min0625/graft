// Copyright 2026 The Graft Authors

//go:build windows

package vendordir

import (
	"fmt"
	"os/exec"
	"syscall"
)

// linkDir creates a directory junction at link pointing at target (spec §5.6).
// A junction — not a symlink — is used because it needs no administrator
// privilege. mklink is a cmd builtin, so it runs through cmd /c; CmdLine is set
// explicitly to keep cmd's own parsing from mangling paths (which can never
// contain the quote characters used here).
func linkDir(target, link string) error {
	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: fmt.Sprintf(`/c mklink /J "%s" "%s"`, link, target),
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create junction: %w: %s", err, out)
	}

	return nil
}
