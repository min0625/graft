// Copyright 2026 The Graft Authors

// Package config reads, validates, and writes the graft.toml manifest
// (spec §3.1) and locates the project root (spec §4.1).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/min0625/graft/internal/clierr"
)

// Filename is the manifest file name that marks a project root.
const Filename = "graft.toml"

// Manifest represents graft.toml.
type Manifest struct {
	// Dir is the root directory for installed deps, chosen at `graft init`.
	Dir  string `toml:"dir"`
	Deps []Dep  `toml:"deps,omitempty"`
}

// Dep is one [[deps]] entry of the manifest.
type Dep struct {
	Name    string `toml:"name"`
	Repo    string `toml:"repo"`
	Version string `toml:"version"`        // git tag, or pseudo-version for untagged commits
	Path    string `toml:"path,omitempty"` // optional: subdirectory of the remote repo
}

var nameSegRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Load reads, parses, and validates the manifest at path.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The path is the project's own graft.toml.
	if err != nil {
		return nil, clierr.New(clierr.CodeConfig,
			"cannot read "+Filename,
			err.Error(),
		)
	}

	var m Manifest

	meta, err := toml.Decode(string(data), &m)
	if err != nil {
		return nil, clierr.New(clierr.CodeConfig,
			"invalid "+Filename,
			err.Error(),
		)
	}

	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}

		return nil, clierr.New(clierr.CodeConfig,
			"invalid "+Filename,
			"unknown key(s): "+strings.Join(keys, ", "),
		)
	}

	for i := range m.Deps {
		m.Deps[i].Path = strings.TrimSuffix(m.Deps[i].Path, "/")
	}

	if err := m.Validate(); err != nil {
		return nil, err
	}

	return &m, nil
}

// Write validates m and writes it to path as TOML. Deps are written sorted
// by name regardless of insertion order, so the file stays stable across
// runs and is easy to diff.
func (m *Manifest) Write(path string) error {
	if err := m.Validate(); err != nil {
		return err
	}

	sorted := *m
	sorted.Deps = slices.Clone(m.Deps)

	slices.SortFunc(sorted.Deps, func(a, b Dep) int {
		return strings.Compare(a.Name, b.Name)
	})

	var b strings.Builder

	enc := toml.NewEncoder(&b)
	enc.Indent = ""

	if err := enc.Encode(sorted); err != nil {
		return fmt.Errorf("encode %s: %w", Filename, err)
	}

	//nolint:gosec // The manifest is world-readable by design.
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", Filename, err)
	}

	return nil
}

// FindDep returns a pointer to the dep named name, or nil.
func (m *Manifest) FindDep(name string) *Dep {
	for i := range m.Deps {
		if m.Deps[i].Name == name {
			return &m.Deps[i]
		}
	}

	return nil
}

// ResolvedDest returns d's install path relative to the project root,
// slash-separated: <vendor>/<name>.
func (m *Manifest) ResolvedDest(d Dep) string {
	return path.Join(m.Dir, d.Name)
}

// Validate checks every rule of spec §3.1 and §7 (path safety). Violations
// are reported as exit-2 errors.
func (m *Manifest) Validate() error {
	if m.Dir == "" {
		return clierr.New(clierr.CodeConfig,
			"invalid "+Filename,
			`the required "dir" field is missing`,
			"run `graft init` in a new project to create a valid manifest",
		)
	}

	if err := ValidatePath("dir", m.Dir); err != nil {
		return err
	}

	if err := m.validateDeps(); err != nil {
		return err
	}

	return m.validateDests()
}

func (m *Manifest) validateDeps() error {
	seen := make(map[string]bool, len(m.Deps))

	for _, d := range m.Deps {
		if err := ValidateName(d.Name); err != nil {
			return err
		}

		if seen[d.Name] {
			return clierr.New(clierr.CodeConfig,
				fmt.Sprintf("duplicate dependency name %q", d.Name),
				"every [[deps]] entry must have a unique name",
			)
		}

		seen[d.Name] = true

		if d.Repo == "" {
			return clierr.New(clierr.CodeConfig,
				fmt.Sprintf("dependency %q has no repo", d.Name))
		}

		if d.Version == "" {
			return clierr.New(clierr.CodeConfig,
				fmt.Sprintf("dependency %q has no version", d.Name),
				"set a tag (or run `graft add "+d.Name+"@<ref>`), then run `graft lock`",
			)
		}

		if d.Path != "" {
			if err := ValidatePath(fmt.Sprintf("deps.%s.path", d.Name), d.Path); err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *Manifest) validateDests() error {
	dests := make(map[string]string, len(m.Deps)) // resolved dest → dep name

	for _, d := range m.Deps {
		dest := m.ResolvedDest(d)

		for other, otherName := range dests {
			if dest == other || isAncestor(dest, other) || isAncestor(other, dest) {
				return clierr.New(clierr.CodeConfig,
					fmt.Sprintf("dependencies %q and %q have overlapping install paths", otherName, d.Name),
					fmt.Sprintf("%q and %q overlap; every dep needs its own directory", other, dest),
				)
			}
		}

		dests[dest] = d.Name
	}

	return nil
}

// isAncestor reports whether slash-separated path a is a strict ancestor
// directory of b.
func isAncestor(a, b string) bool {
	return strings.HasPrefix(b, a+"/")
}

// ValidatePath rejects values that could escape the repository or break on
// some platform: absolute paths, "..", ".", backslashes, and drive colons
// (spec §7). field names the offending field in the error message.
func ValidatePath(field, p string) error {
	reject := func(reason string) error {
		return clierr.New(clierr.CodeConfig,
			fmt.Sprintf("invalid %s %q", field, p),
			reason,
		)
	}

	if p == "." || p == "" {
		return reject("the value must name a directory inside the repository, not the repository root")
	}

	if strings.ContainsAny(p, `\:`) {
		return reject(`paths must be relative, slash-separated, and contain no "\" or ":"`)
	}

	if strings.HasPrefix(p, "/") {
		return reject("paths must be relative to the project root, not absolute")
	}

	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return reject(`paths must not contain empty, "." or ".." segments`)
		}
	}

	return nil
}

// FindRoot walks up from start to the nearest directory containing
// graft.toml without crossing a git repository boundary, and returns it.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, Filename)); err == nil {
			return dir, nil
		}

		// The first directory containing .git is the last one tried.
		_, gitErr := os.Stat(filepath.Join(dir, ".git"))
		atBoundary := gitErr == nil || !errors.Is(gitErr, fs.ErrNotExist)

		parent := filepath.Dir(dir)
		if atBoundary || parent == dir {
			return "", clierr.New(clierr.CodeConfig,
				Filename+" not found",
				"run `graft init` first",
			)
		}

		dir = parent
	}
}

// DefaultName derives a dep name from its repo path: the last path segment,
// minus any ".git" suffix.
func DefaultName(repo string) string {
	name := strings.TrimSuffix(repo, "/")

	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		name = name[i+1:]
	}

	return strings.TrimSuffix(name, ".git")
}

// ValidateName reports an exit-2 error when name is not a valid dependency
// name (spec §3.1): each slash-separated segment must match [A-Za-z0-9._-]+
// and not be "." or "..". A single segment (no slash) is a simple name;
// multiple segments (e.g. "tool-a/util") form a path that also determines
// the install location under vendor.
func ValidateName(name string) error {
	for seg := range strings.SplitSeq(name, "/") {
		if !nameSegRe.MatchString(seg) || seg == "." || seg == ".." {
			return clierr.New(clierr.CodeConfig,
				fmt.Sprintf("invalid dependency name %q", name),
				"names must match [A-Za-z0-9._-]+, with / allowed as a path separator (e.g. tool-a/util)",
			)
		}
	}

	return nil
}
