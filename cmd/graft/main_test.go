// Copyright 2026 The Graft Authors

package main

import (
	"bytes"
	"strings"
	"testing"
)

func execute(t *testing.T, args ...string) string {
	t.Helper()

	var out bytes.Buffer

	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}

	return out.String()
}

func TestRootCmd_help(t *testing.T) {
	t.Parallel()

	out := execute(t, "--help")

	if !strings.Contains(out, "graft is a language-agnostic dependency manager") {
		t.Errorf("--help output missing description:\n%s", out)
	}
}

func TestRootCmd_version(t *testing.T) {
	t.Parallel()

	out := execute(t, "--version")

	want := "graft version dev\n"
	if out != want {
		t.Errorf("--version output = %q, want %q", out, want)
	}
}

func TestRootCmd_noArgsShowsHelp(t *testing.T) {
	t.Parallel()

	out := execute(t)

	if !strings.Contains(out, "Usage:") {
		t.Errorf("bare invocation should print help, got:\n%s", out)
	}
}
