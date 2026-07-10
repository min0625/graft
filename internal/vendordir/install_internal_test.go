// Copyright 2026 The Graft Authors

package vendordir

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/hasher"
	"github.com/min0625/graft/internal/lockfile"
)

// TestParkExisting_movesExistingAside verifies the happy path: an existing
// dest is renamed into staging, clearing dest for the caller's own new→dest
// rename.
func TestParkExisting_movesExistingAside(t *testing.T) {
	t.Parallel()

	staging := t.TempDir()
	dest := filepath.Join(t.TempDir(), "dest")

	if err := os.MkdirAll(dest, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dest, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	old, err := parkExisting(staging, dest, "dep", 0)
	if err != nil {
		t.Fatalf("parkExisting: %v", err)
	}

	if old == "" {
		t.Error("parkExisting reported no park path for an existing dest")
	}

	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("dest still exists after parking: %v", err)
	}

	if want := filepath.Join(staging, "old-0"); old != want {
		t.Errorf("park path = %q, want %q", old, want)
	}

	data, err := os.ReadFile(filepath.Join(old, "keep.txt")) //nolint:gosec // Test-controlled path.
	if err != nil || string(data) != "keep" {
		t.Errorf("parked content = %q, %v, want the original file intact", data, err)
	}
}

// TestParkExisting_missingDestIsNoOp verifies parking a dep that has not been
// installed yet (no existing dest) is a no-op, not an error.
func TestParkExisting_missingDestIsNoOp(t *testing.T) {
	t.Parallel()

	staging := t.TempDir()
	dest := filepath.Join(t.TempDir(), "does-not-exist")

	old, err := parkExisting(staging, dest, "dep", 0)
	if err != nil {
		t.Fatalf("parkExisting on a missing dest: %v", err)
	}

	if old != "" {
		t.Errorf("park path = %q, want empty for a missing dest", old)
	}
}

// TestParkExisting_lstatErrorPropagates is the regression test for the bug
// where parkExisting treated any os.Lstat error — not just "does not exist"
// — as "nothing to park", silently skipping both the cwd-inside check and
// the backup/restore protection. It mocks the package-level lstatPath var
// (like the renameFile-mocking tests below) instead of provoking a real OS
// error: the error a blocked path component actually produces isn't
// portable — POSIX gives ENOTDIR, but Windows collapses it into the same
// ERROR_PATH_NOT_FOUND that os.IsNotExist already treats as "missing".
func TestParkExisting_lstatErrorPropagates(t *testing.T) {
	staging := t.TempDir()
	dest := filepath.Join(t.TempDir(), "dest")

	orig := lstatPath
	lstatPath = func(string) (os.FileInfo, error) {
		return nil, &os.PathError{Op: "lstat", Path: dest, Err: syscall.EACCES}
	}

	t.Cleanup(func() { lstatPath = orig })

	if _, err := parkExisting(staging, dest, "dep", 0); err == nil {
		t.Fatal("parkExisting succeeded despite a non-\"not exist\" Lstat failure, want an error")
	}
}

// TestParkExisting_renameFailurePreservesDest is the regression test for the
// medium-severity bug: a failed reinstall must never destroy a currently-
// valid dest. It mocks renameFile to fail rather than provoking a real OS
// rename failure — a blocking file at the rename target does not reliably
// fail the rename on every OS (Windows' MoveFileEx replace semantics differ
// from POSIX rename(2) here). Before this fix, a rename failure fell back to
// deleting dest in place (gitrun.RemoveAll), which could fail partway
// through a large tree and leave it half-gone; now it must return an error
// with dest fully intact.
func TestParkExisting_renameFailurePreservesDest(t *testing.T) {
	staging := t.TempDir()
	dest := filepath.Join(t.TempDir(), "dest")

	if err := os.MkdirAll(dest, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dest, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	orig := renameFile
	renameFile = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EACCES}
	}

	t.Cleanup(func() { renameFile = orig })

	if _, err := parkExisting(staging, dest, "dep", 0); err == nil {
		t.Fatal("parkExisting succeeded despite a blocked rename target, want an error")
	}

	data, err := os.ReadFile(filepath.Join(dest, "keep.txt")) //nolint:gosec // Test-controlled path.
	if err != nil || string(data) != "keep" {
		t.Errorf("dest was disturbed by the failed park: content=%q err=%v", data, err)
	}
}

// TestParkExisting_symlinkDestSkipsCwdCheck is the regression test for the
// bug where parking a link-mode dest (a symlink/junction into the store)
// falsely rejected a cwd resolving through that very link: the process's cwd
// refers to the link's target, which renaming the link itself never
// disturbs, so the cwd check must not apply to a link dest. It uses linkDir —
// the exact node link mode installs, a junction on Windows — because a
// junction's Lstat mode is ModeIrregular, not ModeSymlink, so an os.Symlink
// fixture alone could not catch a mode-bit-based check going wrong.
func TestParkExisting_symlinkDestSkipsCwdCheck(t *testing.T) {
	staging := t.TempDir()
	target := t.TempDir()
	dest := filepath.Join(t.TempDir(), "dest-link")

	if err := linkDir(target, dest); err != nil {
		t.Skip("link creation not supported:", err)
	}

	// Simulate a shell that cd'd into the dep through the link: $PWD is the
	// link path itself, exactly what a process's cwd looks like inside a
	// link-mode dest.
	t.Chdir(dest)

	old, err := parkExisting(staging, dest, "dep", 0)
	if err != nil {
		t.Fatalf("parkExisting on a link dest whose target is cwd: %v", err)
	}

	got, err := os.Readlink(old)
	if err != nil || cleanLinkTarget(got) != cleanLinkTarget(target) {
		t.Errorf("parked link target = %q, %v, want %q", got, err, target)
	}
}

// TestRemoveExtras_linkExtraSkipsCwdCheck is the regression test for the same
// false rejection in removeExtras: removing a link-mode extra (a dep dropped
// from the manifest) while the shell's cwd resolves through the link must
// succeed — unlinking the node never disturbs that cwd, which refers to the
// link's target.
func TestRemoveExtras_linkExtraSkipsCwdCheck(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()

	vendorAbs := filepath.Join(root, "deps")
	if err := os.MkdirAll(vendorAbs, 0o750); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(vendorAbs, "dropped")
	if err := linkDir(target, link); err != nil {
		t.Skip("link creation not supported:", err)
	}

	t.Chdir(link)

	removed, err := removeExtras(root, "deps", nil)
	if err != nil {
		t.Fatalf("removeExtras on a link extra whose target is cwd: %v", err)
	}

	if len(removed) != 1 || removed[0] != "deps/dropped" {
		t.Errorf("removed = %v, want [deps/dropped]", removed)
	}

	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("link extra still exists after removeExtras: %v", err)
	}
}

// TestParkExisting_crossDeviceFallsBackToDelete is the regression test for
// the bug where parkExisting lost the old cross-filesystem (EXDEV) fallback:
// a dest mounted on a different filesystem than staging (a custom dest
// outside the vendor root's filesystem) can never be renamed aside, so it
// must be deleted in place instead of failing every future reinstall.
func TestParkExisting_crossDeviceFallsBackToDelete(t *testing.T) {
	staging := t.TempDir()
	dest := filepath.Join(t.TempDir(), "dest")

	if err := os.MkdirAll(dest, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dest, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	orig := renameFile
	renameFile = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
	}

	t.Cleanup(func() { renameFile = orig })

	old, err := parkExisting(staging, dest, "dep", 0)
	if err != nil {
		t.Fatalf("parkExisting on a simulated cross-device rename: %v", err)
	}

	if old != "" {
		t.Errorf("park path = %q, want empty — EXDEV falls back to deleting dest in place", old)
	}

	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("dest still exists after the EXDEV fallback delete: %v", err)
	}
}

// TestIsCrossDevice is the regression test for the bug where the EXDEV
// fallback never triggered on Windows: Go's syscall.EXDEV there is a
// synthetic APPLICATION_ERROR-based value, distinct from the real
// ERROR_NOT_SAME_DEVICE (17) MoveFileEx returns for an actual cross-volume
// rename, so errors.Is(err, syscall.EXDEV) alone never matched it.
func TestIsCrossDevice(t *testing.T) {
	t.Parallel()

	if !isCrossDevice(&os.LinkError{Err: syscall.EXDEV}) {
		t.Error("isCrossDevice(syscall.EXDEV) = false, want true")
	}

	if isCrossDevice(&os.LinkError{Err: syscall.Errno(0)}) {
		t.Error("isCrossDevice(Errno(0)) = true, want false")
	}

	if runtime.GOOS == windowsGOOS && !isCrossDevice(&os.LinkError{Err: syscall.Errno(errnoNotSameDeviceWindows)}) {
		t.Error("isCrossDevice(ERROR_NOT_SAME_DEVICE) = false, want true on Windows")
	}
}

// TestRestoreParked verifies restoreParked moves a parked dest back in place,
// and is a no-op when nothing was parked — the mechanism a failed swap-in
// after a successful park relies on to avoid losing the previously-valid
// dest (see parkExisting).
func TestRestoreParked(t *testing.T) {
	t.Parallel()

	t.Run("moves parked dest back", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		old := filepath.Join(dir, "old")
		dest := filepath.Join(dir, "dest")

		if err := os.WriteFile(old, []byte("parked"), 0o600); err != nil {
			t.Fatal(err)
		}

		restoreParked(old, dest)

		data, err := os.ReadFile(dest) //nolint:gosec // Test-controlled path.
		if err != nil || string(data) != "parked" {
			t.Errorf("dest = %q, %v, want the parked content restored", data, err)
		}
	})

	t.Run("no-op when nothing was parked", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		dest := filepath.Join(dir, "dest")

		restoreParked("", dest)

		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Errorf("restoreParked with no parked path created dest unexpectedly")
		}
	})

	// Regression test for the orphaned-backup bug: restoreParked must clear a
	// partial dest a failed copy fallback left behind, since neither OS
	// allows renaming a directory onto an existing non-empty one.
	t.Run("clears a partial dest before restoring", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		old := filepath.Join(dir, "old")
		dest := filepath.Join(dir, "dest")

		if err := os.MkdirAll(old, 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(old, "keep.txt"), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.MkdirAll(dest, 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dest, "partial.txt"), []byte("half"), 0o600); err != nil {
			t.Fatal(err)
		}

		restoreParked(old, dest)

		data, err := os.ReadFile(filepath.Join(dest, "keep.txt")) //nolint:gosec // Test-controlled path.
		if err != nil || string(data) != "keep" {
			t.Errorf("dest = %q, %v, want the parked tree restored over the partial one", data, err)
		}
	})
}

// TestRestoreParked_backsUpWhenRestoreRenameFails is the regression test for
// the lost-backup bug: if the restore-back rename itself fails (e.g. dest
// still locked on Windows), the parked tree must survive at
// dest+".graft-backup" instead of being silently deleted along with staging
// once Reconcile's unconditional cleanup runs. Not t.Parallel(): it mutates
// the package-level renameFile var, like the other renameFile-mocking tests
// in this file.
func TestRestoreParked_backsUpWhenRestoreRenameFails(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old")
	dest := filepath.Join(dir, "dest")

	if err := os.WriteFile(old, []byte("parked"), 0o600); err != nil {
		t.Fatal(err)
	}

	orig := renameFile
	renameFile = func(oldpath, newpath string) error {
		if newpath == dest {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.Errno(1)}
		}

		return orig(oldpath, newpath)
	}

	t.Cleanup(func() { renameFile = orig })

	restoreParked(old, dest)

	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("dest = %v, want it to remain absent since the restore rename failed", err)
	}

	data, err := os.ReadFile(dest + ".graft-backup") //nolint:gosec // Test-controlled path.
	if err != nil || string(data) != "parked" {
		t.Errorf("backup content = %q, %v, want the parked tree preserved at dest+\".graft-backup\"", data, err)
	}
}

// TestRestoreParked_replacesStaleBackup is the regression test for the
// lost-backup bug's second act: a stale <dest>.graft-backup left by a
// previous failed restore blocked the backup rename (renaming onto an
// existing non-empty directory fails on every OS, and a failing install
// returns before removeExtras ever reaps the stale copy), so the freshly
// parked tree was silently wiped with staging. The stale backup is by
// definition older and must be replaced. Not t.Parallel(): it mutates the
// package-level renameFile var.
func TestRestoreParked_replacesStaleBackup(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old")
	dest := filepath.Join(dir, "dest")
	backup := dest + ".graft-backup"

	if err := os.WriteFile(old, []byte("parked"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(backup, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(backup, "stale.txt"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	orig := renameFile
	renameFile = func(oldpath, newpath string) error {
		if newpath == dest {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.Errno(1)}
		}

		return orig(oldpath, newpath)
	}

	t.Cleanup(func() { renameFile = orig })

	restoreParked(old, dest)

	data, err := os.ReadFile(backup) //nolint:gosec // Test-controlled path.
	if err != nil || string(data) != "parked" {
		t.Errorf("backup = %q, %v, want the stale backup replaced by the parked tree", data, err)
	}
}

// TestCwdInsideErr covers the low-severity bug: Windows refuses to
// delete/rename a directory that is the process's own working directory,
// which otherwise surfaces as a confusing low-level OS error. cwdInsideErr
// must catch this upfront with a clear graft-level message.
func TestCwdInsideErr(t *testing.T) {
	target := t.TempDir()

	t.Run("cwd is target", func(t *testing.T) {
		t.Chdir(target)

		err := cwdInsideErr("replace", "dep", target)
		assertCwdInsideErr(t, err, "replace", "dep")
	})

	t.Run("cwd is inside target", func(t *testing.T) {
		nested := filepath.Join(target, "sub")
		if err := os.Mkdir(nested, 0o750); err != nil {
			t.Fatal(err)
		}

		t.Chdir(nested)

		err := cwdInsideErr("replace", "dep", target)
		assertCwdInsideErr(t, err, "replace", "dep")
	})

	t.Run("cwd is outside target", func(t *testing.T) {
		t.Chdir(t.TempDir())

		if err := cwdInsideErr("replace", "dep", target); err != nil {
			t.Errorf("cwdInsideErr = %v, want nil for an unrelated cwd", err)
		}
	})

	// spec: REQ-APPLY-CWD-GUARD (message names the action, not just the target).
	t.Run("message names the action, not just the target", func(t *testing.T) {
		t.Chdir(target)

		err := cwdInsideErr("remove", "extra-dir", target)
		assertCwdInsideErr(t, err, "remove", "extra-dir")
	})
}

// TestInstallDep_linkModeCrossDeviceFallsBackToDirectLink is the regression
// test for the bug where link-mode install's staging→dest rename had no
// cross-device fallback: a dest mounted on a different filesystem than
// staging (the same case parkExisting itself falls back for) made the final
// move-into-place fail with no recovery, after the pre-existing dest had
// already been parked/deleted — losing the dependency entirely. It must fall
// back to building the link directly at dest instead, exactly as the
// pre-refactor code did.
func TestInstallDep_linkModeCrossDeviceFallsBackToDirectLink(t *testing.T) {
	staging := t.TempDir()
	linksDir := t.TempDir()
	storePath := t.TempDir()
	destAbs := filepath.Join(t.TempDir(), "dest")

	orig := renameFile
	renameFile = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
	}

	t.Cleanup(func() { renameFile = orig })

	dep := lockfile.LockedDep{Name: "dep", Hash: "sha256:abc"}
	opts := Options{Mode: ModeLink, LinksDir: linksDir}

	if err := installDep(staging, destAbs, dep, 0, opts, storePath); err != nil {
		t.Fatalf("installDep: %v", err)
	}

	got, err := os.Readlink(destAbs)
	if err != nil || got != storePath {
		t.Errorf("dest link target = %q, %v, want %q", got, err, storePath)
	}
}

// TestInstall_crossDeviceRenameFallsBackToCopy verifies install's (copy-mode)
// final staging→dest swap falls back to copyTree when the rename fails with
// EXDEV, mirroring the fallback installLinkDep already has. install used to
// call os.Rename directly here instead of the renameFile indirection every
// other swap-in path uses, so this fallback was previously untestable without
// a real filesystem boundary.
func TestInstall_crossDeviceRenameFallsBackToCopy(t *testing.T) {
	staging := t.TempDir()
	storePath := t.TempDir()
	destAbs := filepath.Join(t.TempDir(), "dest")

	if err := os.WriteFile(filepath.Join(storePath, "file.txt"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}

	hash, err := hasher.HashTree(storePath)
	if err != nil {
		t.Fatal(err)
	}

	orig := renameFile
	renameFile = func(oldpath, newpath string) error {
		if newpath == destAbs {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
		}

		return orig(oldpath, newpath)
	}

	t.Cleanup(func() { renameFile = orig })

	dep := lockfile.LockedDep{Name: "dep", Hash: hash}

	if err := install(staging, storePath, destAbs, dep, 0); err != nil {
		t.Fatalf("install: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destAbs, "file.txt")) //nolint:gosec // Test-controlled path.
	if err != nil || string(data) != "content" {
		t.Errorf("dest content = %q, %v, want the copied tree", data, err)
	}
}

func assertCwdInsideErr(t *testing.T, err error, action, label string) {
	t.Helper()

	if err == nil {
		t.Fatal("cwdInsideErr = nil, want an error")
	}

	var cliErr *clierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}

	if cliErr.Code != clierr.CodeGeneral {
		t.Errorf("exit code = %d, want %d", cliErr.Code, clierr.CodeGeneral)
	}

	if !strings.Contains(cliErr.Summary, action) || !strings.Contains(cliErr.Summary, label) ||
		!strings.Contains(cliErr.Summary, "current directory") {
		t.Errorf("summary = %q, want it to mention %q, %q, and the cwd", cliErr.Summary, action, label)
	}
}
