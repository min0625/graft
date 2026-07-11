// Copyright 2026 The Graft Authors

package clierr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/min0625/graft/internal/clierr"
)

func TestFormat_summaryOnly(t *testing.T) {
	t.Parallel()

	err := clierr.New(clierr.CodeConfig, "graft.toml not found")

	want := "error: graft.toml not found\n"
	if got := clierr.Format(err); got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

func TestFormat_detailParagraphs(t *testing.T) {
	t.Parallel()

	err := clierr.New(
		clierr.CodeConfig,
		"graft.lock is out of sync with graft.toml",
		`dependency "new-scripts" is in graft.toml but not in graft.lock`,
		"run `graft lock` to update the lockfile, then commit it",
	)

	want := "error: graft.lock is out of sync with graft.toml\n" +
		"\n" +
		"  dependency \"new-scripts\" is in graft.toml but not in graft.lock\n" +
		"\n" +
		"  run `graft lock` to update the lockfile, then commit it\n"
	if got := clierr.Format(err); got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

func TestFormat_multilineParagraph(t *testing.T) {
	t.Parallel()

	err := clierr.New(
		clierr.CodeIntegrity,
		`content integrity check failed for "shared-scripts"`,
		"expected  sha256:aaaa\ngot       sha256:bbbb",
	)

	want := "error: content integrity check failed for \"shared-scripts\"\n" +
		"\n" +
		"  expected  sha256:aaaa\n" +
		"  got       sha256:bbbb\n"
	if got := clierr.Format(err); got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

func TestFormat_skipsEmptyDetailParagraphs(t *testing.T) {
	t.Parallel()

	err := clierr.New(clierr.CodeGeneral, "summary", "", "detail")

	want := "error: summary\n" +
		"\n" +
		"  detail\n"
	if got := clierr.Format(err); got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

func TestFormat_typedNilError(t *testing.T) {
	t.Parallel()

	var nilErr *clierr.Error

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "bare", err: nilErr, want: "error: <nil>\n"},
		{name: "wrapped", err: fmt.Errorf("apply: %w", nilErr), want: "error: apply: <nil>\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := clierr.Format(tt.err); got != tt.want {
				t.Errorf("Format(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestFormat_plainError(t *testing.T) {
	t.Parallel()

	err := errors.New("something broke")

	want := "error: something broke\n"
	if got := clierr.Format(err); got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

func TestFormat_wrappedError(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("apply: %w", clierr.New(clierr.CodeNetwork, `could not clone "shared-scripts"`))

	want := "error: could not clone \"shared-scripts\"\n"
	if got := clierr.Format(err); got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}
}

func TestFormat_joinedErrors(t *testing.T) {
	t.Parallel()

	err := errors.Join(
		clierr.New(clierr.CodeIntegrity, `content integrity check failed for "a"`, "expected x"),
		clierr.New(clierr.CodeIntegrity, `content integrity check failed for "b"`),
	)

	want := "error: content integrity check failed for \"a\"\n" +
		"\n" +
		"  expected x\n" +
		"\n" +
		"error: content integrity check failed for \"b\"\n"
	if got := clierr.Format(err); got != want {
		t.Errorf("Format() = %q, want %q", got, want)
	}

	if got := clierr.ExitCode(err); got != int(clierr.CodeIntegrity) {
		t.Errorf("ExitCode() = %d, want %d", got, clierr.CodeIntegrity)
	}
}

// spec: REQ-EXIT-MAXCODE
func TestExitCode_joinedErrorsHighestCodeWins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "integrity beats network regardless of order",
			err: errors.Join(
				clierr.New(clierr.CodeNetwork, `could not clone "a"`),
				clierr.New(clierr.CodeIntegrity, `content integrity check failed for "b"`),
			),
			want: 4,
		},
		{
			name: "integrity first, network second",
			err: errors.Join(
				clierr.New(clierr.CodeIntegrity, `content integrity check failed for "a"`),
				clierr.New(clierr.CodeNetwork, `could not clone "b"`),
			),
			want: 4,
		},
		{
			name: "clierr code beats a plain error",
			err: errors.Join(
				errors.New("boom"),
				clierr.New(clierr.CodeConfig, "bad manifest"),
			),
			want: 2,
		},
		{
			name: "joined plain errors are a general error",
			err:  errors.Join(errors.New("a"), errors.New("b")),
			want: 1,
		},
		{
			name: "wrapped joined errors",
			err: fmt.Errorf("apply: %w", errors.Join(
				clierr.New(clierr.CodeNetwork, `could not clone "a"`),
				clierr.New(clierr.CodeIntegrity, `content integrity check failed for "b"`),
			)),
			want: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := clierr.ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: 0},
		{name: "plain error", err: errors.New("boom"), want: 1},
		{name: "general", err: clierr.New(clierr.CodeGeneral, "boom"), want: 1},
		{name: "config", err: clierr.New(clierr.CodeConfig, "boom"), want: 2},
		{name: "network", err: clierr.New(clierr.CodeNetwork, "boom"), want: 3},
		{name: "integrity", err: clierr.New(clierr.CodeIntegrity, "boom"), want: 4},
		{name: "wrapped", err: fmt.Errorf("apply: %w", clierr.New(clierr.CodeIntegrity, "boom")), want: 4},
		{name: "typed nil", err: fmt.Errorf("apply: %w", (*clierr.Error)(nil)), want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := clierr.ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
