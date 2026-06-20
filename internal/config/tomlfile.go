// Copyright 2026 The Graft Authors

package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// AppendDep adds a new [[deps]] block for dep to the TOML file at filePath,
// keeping the file sorted by name. Existing entries keep their comments, blank
// lines, and field formatting (spec §3.1).
func AppendDep(filePath string, dep Dep) error {
	data, err := os.ReadFile(filePath) //nolint:gosec // The path is the project's own graft.toml.
	if err != nil {
		return fmt.Errorf("read %s: %w", Filename, err)
	}

	existing := string(data)

	// Ensure at least one blank line before the new block; sortDeps then moves
	// it (with that blank as its preamble) into its sorted position.
	sep := "\n"
	if strings.HasSuffix(existing, "\n\n") {
		sep = ""
	} else if !strings.HasSuffix(existing, "\n") {
		sep = "\n\n"
	}

	appended := existing + sep + formatDepBlock(dep)

	return writeFile(filePath, sortDeps(appended))
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

	return writeFile(filePath, sortDeps(strings.Join(result, "\n")))
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

	return writeFile(filePath, sortDeps(strings.Join(result, "\n")))
}

// sortDeps reorders the [[deps]] blocks in content by name and returns it with a
// single trailing newline. Each block keeps its preamble — the comments and
// blank line that parseDepBlocks attaches above it — so comments travel with
// their entry, like go.mod (spec §3.1). Everything before the first block (the
// header and dir line) is left in place.
func sortDeps(content string) string {
	lines := strings.Split(content, "\n")

	// Drop trailing blank lines (including the empty element Split leaves after a
	// trailing newline) so a reordered last block does not gain a double blank.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	blocks := parseDepBlocks(lines)
	if len(blocks) < 2 {
		return strings.Join(lines, "\n") + "\n"
	}

	order := slices.Clone(blocks)
	slices.SortStableFunc(order, func(a, b depBlock) int {
		return strings.Compare(a.name, b.name)
	})

	out := slices.Clone(lines[:blocks[0].start])
	for _, blk := range order {
		out = append(out, lines[blk.start:blk.end]...)
	}

	return strings.Join(out, "\n") + "\n"
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

// updateDepLines rewrites the repo/version/subdir (and optionally name) key-value
// lines within a block's slice of lines to match dep, leaving all other lines
// (comments, blank lines, unknown keys) intact.
func updateDepLines(blockLines []string, dep Dep) []string {
	result := make([]string, 0, len(blockLines))

	subdirSeen := false
	symlinksSeen := false
	versionResultIdx := -1 // index in result where the version line was placed

	for _, line := range blockLines {
		switch {
		case matchesTomlKey(line, "name"):
			result = append(result, leadingSpace(line)+`name = "`+dep.Name+`"`+inlineComment(line))
		case matchesTomlKey(line, "repo"):
			result = append(result, leadingSpace(line)+`repo = "`+dep.Repo+`"`+inlineComment(line))
		case matchesTomlKey(line, "version"):
			versionResultIdx = len(result)
			result = append(result, leadingSpace(line)+`version = "`+dep.Version+`"`+inlineComment(line))
		case matchesTomlKey(line, "subdir"):
			subdirSeen = true

			if dep.Subdir != "" {
				result = append(result, leadingSpace(line)+`subdir = "`+dep.Subdir+`"`+inlineComment(line))
			}
			// dep.Subdir == "" means the subdir key is being removed — skip the line.
		case matchesTomlKey(line, "symlinks"):
			symlinksSeen = true

			if dep.Symlinks != "" {
				result = append(result, leadingSpace(line)+`symlinks = "`+dep.Symlinks+`"`+inlineComment(line))
			}
			// dep.Symlinks == "" means the key is being removed — skip the line.
		default:
			result = append(result, line)
		}
	}

	// If subdir wasn't in the block but is now required, insert it after version.
	if !subdirSeen && dep.Subdir != "" {
		subdirLine := `subdir = "` + dep.Subdir + `"`
		if versionResultIdx >= 0 {
			result = slices.Insert(result, versionResultIdx+1, subdirLine)
		} else {
			result = append(result, subdirLine)
		}
	}

	// If symlinks wasn't in the block but is now set, append it.
	if !symlinksSeen && dep.Symlinks != "" {
		result = append(result, `symlinks = "`+dep.Symlinks+`"`)
	}

	return result
}

// formatDepBlock returns a TOML [[deps]] block for dep, newline-terminated.
func formatDepBlock(dep Dep) string {
	s := "[[deps]]\n" +
		`name = "` + dep.Name + `"` + "\n" +
		`repo = "` + dep.Repo + `"` + "\n" +
		`version = "` + dep.Version + `"` + "\n"

	if dep.Subdir != "" {
		s += `subdir = "` + dep.Subdir + `"` + "\n"
	}

	if dep.Symlinks != "" {
		s += `symlinks = "` + dep.Symlinks + `"` + "\n"
	}

	return s
}

// parseTomlString returns the double-quoted string value for key on line, e.g.
// `name = "foo"` or `name    = "foo"` → "foo". Tolerates any whitespace around
// the `=` and a trailing inline comment (`name = "foo" # note`). Returns
// ("", false) when the line does not match.
func parseTomlString(line, key string) (string, bool) {
	trimmed := strings.TrimSpace(line)

	k, rest, found := strings.Cut(trimmed, "=")
	if !found || strings.TrimSpace(k) != key {
		return "", false
	}

	rest = strings.TrimSpace(rest)
	if len(rest) == 0 || rest[0] != '"' {
		return "", false
	}

	// The value ends at the closing quote; anything after it (an inline comment)
	// is ignored. ponytail: no escaped-quote handling — graft names/repos/
	// versions/paths can never contain a double quote.
	end := strings.IndexByte(rest[1:], '"')
	if end < 0 {
		return "", false
	}

	return rest[1 : 1+end], true
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

// inlineComment returns the text after a quoted value's closing quote — the
// whitespace and inline comment, e.g. ` # note` for `name = "x" # note` — so a
// rewritten line keeps it. Returns "" when there is none.
func inlineComment(line string) string {
	open := strings.IndexByte(line, '"')
	if open < 0 {
		return ""
	}

	rel := strings.IndexByte(line[open+1:], '"')
	if rel < 0 {
		return ""
	}

	return line[open+1+rel+1:] // everything after the closing quote
}

// writeFile writes content to filePath.
func writeFile(filePath, content string) error {
	//nolint:gosec // The manifest is world-readable by design.
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", Filename, err)
	}

	return nil
}
