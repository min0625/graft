// Copyright 2026 The Graft Authors

package projlock_test

import (
	"context"
	"io"
	"testing"

	"github.com/min0625/graft/internal/cachedir"
	"github.com/min0625/graft/internal/projlock"
)

func TestAcquire_lockAndRelease(t *testing.T) {
	t.Setenv(cachedir.EnvOverride, t.TempDir())

	release, err := projlock.Acquire(t.Context(), t.TempDir(), io.Discard)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	release() // must not panic
}

func TestAcquire_sequential(t *testing.T) {
	t.Setenv(cachedir.EnvOverride, t.TempDir())

	root := t.TempDir()

	release, err := projlock.Acquire(t.Context(), root, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	release()

	// After release a second acquire on the same root must succeed.
	release2, err := projlock.Acquire(t.Context(), root, io.Discard)
	if err != nil {
		t.Fatalf("second Acquire after release: %v", err)
	}

	release2()
}

func TestAcquire_differentRoots(t *testing.T) {
	t.Setenv(cachedir.EnvOverride, t.TempDir())

	// Two different project roots must use independent lock files.
	rel1, err := projlock.Acquire(t.Context(), t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	defer rel1()

	rel2, err := projlock.Acquire(t.Context(), t.TempDir(), io.Discard)
	if err != nil {
		t.Fatalf("Acquire on different root while first held: %v", err)
	}

	defer rel2()
}

func TestAcquire_canceledContext(t *testing.T) {
	t.Setenv(cachedir.EnvOverride, t.TempDir())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Lock is available, so TryLock succeeds before ctx.Done() is polled.
	// The test just verifies it returns without hanging.
	release, err := projlock.Acquire(ctx, t.TempDir(), io.Discard)
	if err != nil {
		// Acceptable: implementation may check ctx before TryLock.
		return
	}

	release()
}
