// Copyright 2026 The Graft Authors

package vendordir

import "testing"

// TestCleanLinkTarget checks that a Windows junction's extended-length
// prefix is stripped before comparison, and that an ordinary path is only
// Clean-normalized.
func TestCleanLinkTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{`\\?\C:\store\ab\cd1234`, `C:\store\ab\cd1234`},
		{"/store/ab/cd1234", "/store/ab/cd1234"},
		{"/store/ab//cd1234", "/store/ab/cd1234"},
		{"/store/ab/./cd1234", "/store/ab/cd1234"},
	}

	for _, tt := range tests {
		if got := cleanLinkTarget(tt.in); got != tt.want {
			t.Errorf("cleanLinkTarget(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
