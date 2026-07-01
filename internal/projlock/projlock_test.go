// Copyright 2026 The Graft Authors

package projlock_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

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

// TestAcquire_blocksUntilContextCanceled verifies that a second Acquire on an
// already-held lock blocks and returns ctx's error once the context is
// canceled, instead of failing immediately or hanging forever.
func TestAcquire_blocksUntilContextCanceled(t *testing.T) {
	t.Setenv(cachedir.EnvOverride, t.TempDir())

	root := t.TempDir()

	release1, err := projlock.Acquire(t.Context(), root, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer release1()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()

	_, err = projlock.Acquire(ctx, root, io.Discard)
	if err == nil {
		t.Fatal("second Acquire on a held lock succeeded, want the context deadline error")
	}

	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("Acquire returned after %v, want it to have blocked until the deadline", elapsed)
	}
}

// TestAcquire_printsWaitHintAfterOneSecond verifies the cargo/uv-style hint is
// printed once contention has lasted over a second, and that the waiting
// caller still succeeds once the lock is released.
func TestAcquire_printsWaitHintAfterOneSecond(t *testing.T) {
	t.Setenv(cachedir.EnvOverride, t.TempDir())

	root := t.TempDir()

	release1, err := projlock.Acquire(t.Context(), root, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(1200 * time.Millisecond)
		release1()
	}()

	var buf bytes.Buffer

	release2, err := projlock.Acquire(t.Context(), root, &buf)
	if err != nil {
		t.Fatalf("Acquire after contention: %v", err)
	}
	defer release2()

	if !strings.Contains(buf.String(), "waiting for another graft process") {
		t.Errorf("warn output = %q, want it to mention waiting for another process", buf.String())
	}
}
