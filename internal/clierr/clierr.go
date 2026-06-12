// Copyright 2026 The Graft Authors

// Package clierr defines graft's process exit codes (spec §4.5) and the shared
// error type every command uses to report failures on stderr in the Cargo-style
// format of spec §6: what went wrong, followed by what to do next.
package clierr

import (
	"errors"
	"strings"
)

// Code is a process exit code as defined in spec §4.5.
type Code int

const (
	// CodeSuccess means the command succeeded.
	CodeSuccess Code = 0
	// CodeGeneral is a general error (e.g. a nonexistent ref).
	CodeGeneral Code = 1
	// CodeConfig is a config/lockfile validation error.
	CodeConfig Code = 2
	// CodeNetwork is a network error.
	CodeNetwork Code = 3
	// CodeIntegrity is a hash mismatch (content integrity failure).
	CodeIntegrity Code = 4
)

// Error carries an exit code, a one-line summary, and optional detail
// paragraphs giving context and the suggested next step.
type Error struct {
	// Code is the process exit code the CLI exits with.
	Code Code
	// Summary is the one-line description shown after "error: ".
	Summary string
	// Details are paragraphs printed indented under the summary, separated
	// by blank lines. Lines within a paragraph are separated by "\n".
	Details []string
}

// New returns an *Error with the given exit code, summary, and detail
// paragraphs.
func New(code Code, summary string, details ...string) *Error {
	return &Error{Code: code, Summary: summary, Details: details}
}

// Error returns the one-line summary.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	return e.Summary
}

// Format renders err for stderr in the spec §6 layout:
//
//	error: <summary>
//
//	  <detail paragraph, indented two spaces>
//
//	  <next detail paragraph>
//
// Errors that do not wrap an *Error render as a bare "error: <err>" line.
// Empty detail paragraphs are skipped.
func Format(err error) string {
	var cliErr *Error

	// The nil check guards against a typed-nil *Error in the chain, which
	// errors.As matches while leaving cliErr nil.
	if !errors.As(err, &cliErr) || cliErr == nil {
		return "error: " + err.Error() + "\n"
	}

	var b strings.Builder

	if cliErr.Summary != "" {
		b.WriteString("error: " + cliErr.Summary + "\n")
	}

	for _, paragraph := range cliErr.Details {
		if paragraph == "" {
			continue
		}

		b.WriteString("\n")

		for line := range strings.Lines(paragraph) {
			b.WriteString("  " + strings.TrimRight(line, "\n") + "\n")
		}
	}

	return b.String()
}

// ExitCode returns the process exit code for err: the embedded Code when err
// wraps an *Error, CodeGeneral for any other non-nil error, and CodeSuccess
// for nil.
func ExitCode(err error) int {
	if err == nil {
		return int(CodeSuccess)
	}

	var cliErr *Error

	if errors.As(err, &cliErr) && cliErr != nil {
		return int(cliErr.Code)
	}

	return int(CodeGeneral)
}
