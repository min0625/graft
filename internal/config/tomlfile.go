// Copyright 2026 The Graft Authors

package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// AppendDep appends a new [[deps]] block for dep at the end of the TOML file
// at filePath, preserving all existing content verbatim (spec §3.1).
func AppendDep(filePath string, dep Dep) error {
	data, err := os.ReadFile(filePath) //nolint:gosec // The path is the project's own graft.toml.
	if err != nil {
		return fmt.Errorf("read %s: %w", Filename, err)
	}

	existing := string(data)

	// Ensure at least one blank line before the new block.
	sep := "\n"
	if strings.HasSuffix(existing, "\n\n") {
		sep = ""
	} else if !strings.HasSuffix(existing, "\n") {
		sep = "\n\n"
	}

	result := existing + sep + formatDepBlock(dep)

	return writeFile(filePath, result)
}

// UpdateDep finds the [[deps]] block with dep.Name in the TOML file at
// filePath and rewrites its repo/version/path key-value lines to match dep,
// preserving all other lines (comments, blank lines, unknown keys) verbatim.
// The name key is also updated in case it was renamed (spec §3.1).
func UpdateDep(filePath string, dep Dep) error {
	data, err := os.ReadFile(filePath) //nolint:gosec // The path is the project's own graft.toml.
	if err != nil {
		return fmt.Errorf("read %s: %w", Filename, err)
	}

	lines := strings.Split(string(data), "\n")
	blocks := parseDepBlocks(lines)

	var target *depBlock

	for i := range blocks {
		if blocks[i].name == dep.Name {
			target = &blocks[i]
			break
		}
	}

	if target == nil {
		return fmt.Errorf("dep %q not found in %s", dep.Name, Filename)
	}

	updated := updateDepLines(lines[target.start:target.end], dep)

	result := make([]string, 0, len(lines)-len(lines[target.start:target.end])+len(updated))
	result = append(result, lines[:target.start]...)
	result = append(result, updated...)
	result = append(result, lines[target.end:]...)

	return writeFile(filePath, strings.Join(result, "\n"))
}

// RemoveDep removes the [[deps]] block with the given name from the TOML file
// at filePath, preserving all other content verbatim (spec §3.1).
func RemoveDep(filePath, name string) error {
	data, err := os.ReadFile(filePath) //nolint:gosec // The path is the project's own graft.toml.
	if err != nil {
		return fmt.Errorf("read %s: %w", Filename, err)
	}

	lines := strings.Split(string(data), "\n")
	blocks := parseDepBlocks(lines)

	var target *depBlock

	for i := range blocks {
		if blocks[i].name == name {
			target = &blocks[i]
			break
		}
	}

	if target == nil {
		return fmt.Errorf("dep %q not found in %s", name, Filename)
	}

	result := make([]string, 0, len(lines)-(target.end-target.start))
	result = append(result, lines[:target.start]...)
	result = append(result, lines[target.end:]...)

	return writeFile(filePath, strings.Join(result, "\n"))
}

// depBlock is the location of a [[deps]] block within a slice of lines.
type depBlock struct {
	name       string // value of the name key within this block
	start, end int    // half-open range: lines[start:end]
}

// parseDepBlocks scans lines and returns the location and name of each [[deps]] block.
//
// Each block includes the blank lines and comment lines immediately preceding its
// [[deps]] header (its "preamble"), so that removing a block also removes the
// comment above it rather than orphaning it. The preamble scan stops at any
// non-blank, non-comment line (e.g. a key-value line in the previous block).
func parseDepBlocks(lines []string) []depBlock {
	// Find all [[deps]] line positions.
	var depsPos []int

	for i, line := range lines {
		if strings.TrimSpace(line) == "[[deps]]" {
			depsPos = append(depsPos, i)
		}
	}

	if len(depsPos) == 0 {
		return nil
	}

	// For each [[deps]], walk backwards to find its preamble start.
	preamble := make([]int, len(depsPos))

	for j, pos := range depsPos {
		start := pos
		for start > 0 {
			trimmed := strings.TrimSpace(lines[start-1])
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				start--
			} else {
				break
			}
		}

		preamble[j] = start
	}

	// Build blocks: block j spans [preamble[j], preamble[j+1]) (or EOF).
	blocks := make([]depBlock, len(depsPos))

	for j, pos := range depsPos {
		blocks[j].start = preamble[j]

		if j+1 < len(depsPos) {
			blocks[j].end = preamble[j+1]
		} else {
			blocks[j].end = len(lines)
		}

		// Extract the name key from the block body.
		for i := pos + 1; i < blocks[j].end; i++ {
			if v, ok := parseTomlString(lines[i], "name"); ok {
				blocks[j].name = v
				break
			}
		}
	}

	return blocks
}

// updateDepLines rewrites the repo/version/path (and optionally name) key-value
// lines within a block's slice of lines to match dep, leaving all other lines
// (comments, blank lines, unknown keys) intact.
func updateDepLines(blockLines []string, dep Dep) []string {
	result := make([]string, 0, len(blockLines))

	pathSeen := false
	versionResultIdx := -1 // index in result where the version line was placed

	for _, line := range blockLines {
		switch {
		case matchesTomlKey(line, "name"):
			result = append(result, leadingSpace(line)+`name = "`+dep.Name+`"`)
		case matchesTomlKey(line, "repo"):
			result = append(result, leadingSpace(line)+`repo = "`+dep.Repo+`"`)
		case matchesTomlKey(line, "version"):
			versionResultIdx = len(result)
			result = append(result, leadingSpace(line)+`version = "`+dep.Version+`"`)
		case matchesTomlKey(line, "path"):
			pathSeen = true

			if dep.Path != "" {
				result = append(result, leadingSpace(line)+`path = "`+dep.Path+`"`)
			}
			// dep.Path == "" means the path key is being removed — skip the line.
		default:
			result = append(result, line)
		}
	}

	// If path wasn't in the block but is now required, insert it after version.
	if !pathSeen && dep.Path != "" {
		pathLine := `path = "` + dep.Path + `"`
		if versionResultIdx >= 0 {
			result = slices.Insert(result, versionResultIdx+1, pathLine)
		} else {
			result = append(result, pathLine)
		}
	}

	return result
}

// formatDepBlock returns a TOML [[deps]] block for dep, newline-terminated.
func formatDepBlock(dep Dep) string {
	s := "[[deps]]\n" +
		`name = "` + dep.Name + `"` + "\n" +
		`repo = "` + dep.Repo + `"` + "\n" +
		`version = "` + dep.Version + `"` + "\n"

	if dep.Path != "" {
		s += `path = "` + dep.Path + `"` + "\n"
	}

	return s
}

// parseTomlString returns the double-quoted string value for key on line, e.g.
// `name = "foo"` or `name    = "foo"` → "foo". Tolerates any whitespace
// around the `=`. Returns ("", false) when the line does not match.
func parseTomlString(line, key string) (string, bool) {
	trimmed := strings.TrimSpace(line)

	k, rest, found := strings.Cut(trimmed, "=")
	if !found || strings.TrimSpace(k) != key {
		return "", false
	}

	rest = strings.TrimSpace(rest)
	if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
		return "", false
	}

	return rest[1 : len(rest)-1], true
}

// matchesTomlKey reports whether line is a TOML key assignment for key.
func matchesTomlKey(line, key string) bool {
	_, ok := parseTomlString(line, key)
	return ok
}

// leadingSpace returns the leading whitespace characters of s.
func leadingSpace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

// writeFile writes content to filePath.
func writeFile(filePath, content string) error {
	//nolint:gosec // The manifest is world-readable by design.
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", Filename, err)
	}

	return nil
}
