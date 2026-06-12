// Copyright 2026 The Graft Authors

package main

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/min0625/graft/internal/projlock"
)

// syncBuffer is a Writer safe for the cross-goroutine use in this file: the
// blocked command writes its wait hint while the test polls for it.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

// TestProjectLock_blocksSecondProcess: while one graft process holds the
// per-project lock, a second mutating command blocks — printing the spec
// §5.7 wait hint after a second — instead of failing, and completes once the
// lock is released. graft status takes no lock and is never blocked.
func TestProjectLock_blocksSecondProcess(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")
	mustRunGraft(t, "apply")

	// Hold the project lock, as a concurrent graft process would.
	release, err := projlock.Acquire(t.Context(), dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	// Read-only status must not block on the held lock (spec §5.7).
	if out := mustRunGraft(t, "status"); !strings.Contains(out, "ok") {
		t.Errorf("status while locked = %q", out)
	}

	var stderr syncBuffer

	done := make(chan error, 1)

	go func() {
		cmd := newRootCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"lock"})

		done <- cmd.ExecuteContext(t.Context())
	}()

	// The wait hint proves the second command is blocked, not failed.
	deadline := time.Now().Add(30 * time.Second)

	for !strings.Contains(stderr.String(), "waiting for another graft process") {
		select {
		case err := <-done:
			t.Fatalf("graft lock finished while the project lock was held (err: %v)", err)
		default:
		}

		if time.Now().After(deadline) {
			t.Fatalf("no wait hint on stderr after 30s; stderr: %q", stderr.String())
		}

		time.Sleep(20 * time.Millisecond)
	}

	release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graft lock after release: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("graft lock still blocked 30s after the project lock was released")
	}

	// Final state is consistent: the blocked lock saw the already-synced
	// lockfile and left it locked at the same commit.
	if lock := readProjectFile(t, dir, "graft.lock"); !strings.Contains(lock, f.v1) {
		t.Errorf("graft.lock does not pin %s:\n%s", f.v1, lock)
	}
}
