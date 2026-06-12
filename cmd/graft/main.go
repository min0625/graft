// Copyright 2026 The Graft Authors

// Command graft is a language-agnostic dependency manager for git repositories.
package main

import (
	"fmt"
	"os"

	"github.com/min0625/graft/internal/clierr"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprint(os.Stderr, clierr.Format(err))
		os.Exit(clierr.ExitCode(err))
	}
}
