// Copyright 2026 The Graft Authors

package gitrun_test

import (
	"testing"

	"github.com/min0625/graft/internal/gitrun"
)

func TestCanonicalRepo(t *testing.T) {
	t.Parallel()

	const canonical = "github.com/org/repo"

	tests := []struct {
		in, want string
	}{
		{"github.com/org/repo", canonical},
		{"github.com/org/repo/", canonical},
		{"github.com/org/repo.git", canonical},
		{"https://github.com/org/repo", canonical},
		{"https://github.com/org/repo.git", canonical},
		{"https://user@github.com/org/repo.git", canonical},
		{"ssh://git@github.com/org/repo.git", canonical},
		{"git@github.com:org/repo.git", canonical},
		{"git@github.com:org/repo", canonical},
		// Spellings that genuinely differ stay distinct.
		{"gitlab.com/org/repo", "gitlab.com/org/repo"},
		{"github.com/org/other", "github.com/org/other"},
	}

	for _, tt := range tests {
		if got := gitrun.CanonicalRepo(tt.in); got != tt.want {
			t.Errorf("CanonicalRepo(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
