// Copyright 2026 The Graft Authors
package main

import (
	"strings"
	"testing"
)

func TestCompletionCmd(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		out, err := runGraft(t, "completion", shell)
		if err != nil {
			t.Fatalf("completion %s: %v", shell, err)
		}

		if !strings.Contains(out, "graft") {
			t.Errorf("completion %s: 'graft' not found in output", shell)
		}
	}
}

func TestCompletionDynamic(t *testing.T) {
	// Subcommands appear when completing the root.
	out, err := runGraft(t, "__complete", "")
	if err != nil {
		t.Fatalf("__complete: %v", err)
	}

	for _, want := range []string{"add", "apply", "status"} {
		if !strings.Contains(out, want) {
			t.Errorf("__complete root: %q not found in output", want)
		}
	}

	// Flags appear when completing after a dash.
	out, err = runGraft(t, "__complete", "apply", "-")
	if err != nil {
		t.Fatalf("__complete apply flags: %v", err)
	}

	if !strings.Contains(out, "--help") {
		t.Errorf("__complete apply: --help not found in output")
	}
}
