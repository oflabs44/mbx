package account

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrAccountExists is returned by AddTemplate when the named account already
// appears (commented or not) in the target file.
var ErrAccountExists = errors.New("account already present in config file")

// ErrAccountAbsent is returned by RemoveAccount when no [accounts.<name>]
// section is present in the file (commented or otherwise).
var ErrAccountAbsent = errors.New("account not present in config file")

// ErrRenameTargetExists is returned by RenameAccount when the destination
// name already appears in the file as an active or commented section.
var ErrRenameTargetExists = errors.New("rename target already present in config file")

// ErrRenameNeedsManualAliasMerge is returned by RenameAccount when the
// source block already has an `aliases =` line — re-emitting one would
// produce duplicate TOML keys. The user merges by hand and re-runs.
var ErrRenameNeedsManualAliasMerge = errors.New("rename refused: account already has an aliases list; merge it by hand and rename again")

const fileBanner = "# mbx config — see https://github.com/oflabs44/mbx/blob/main/docs/config.md\n\n"

func sectionHeader(name string) string { return "[accounts." + name + "]" }

// HasAccount reports whether the file at path contains an `[accounts.<name>]`
// section header, whether commented (`# [accounts.<name>]`) or not. A missing
// file is not an error and reports false — `mbx account add` creates the
// file on first use.
func HasAccount(path, name string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	target := sectionHeader(name)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if headerText(stripComment(sc.Text())) == target {
			return true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	return false, nil
}

// stripComment removes a single leading `#` (with surrounding whitespace) so
// a commented section header matches its uncommented form. Lines without a
// leading `#` are returned unchanged.
func stripComment(line string) string {
	s := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(s, "#") {
		return line
	}
	return strings.TrimLeft(s[1:], " \t")
}

// RemoveAccount comments out the [accounts.<name>] section header and every
// following line that's part of that account's block, stopping at the next
// section header that doesn't belong to the same account. Sub-section
// headers like [accounts.<name>.cache] are still considered part of the
// block (covers any non-idiomatic-but-valid TOML the user might hand-write).
//
// Returns:
//   - (false, nil) when the section was active and is now commented.
//   - (true, nil)  when the section was already commented (no-op).
//   - ErrAccountAbsent (wrapped) when no such section exists.
//
// Does not touch external secret stores. Per CONTEXT.md, secrets in
// 1Password / pass / keychain etc. are the user's to delete.
func RemoveAccount(path, name string) (alreadyCommented bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("%w: %s", ErrAccountAbsent, name)
		}
		return false, fmt.Errorf("reading %s: %w", path, err)
	}

	lines := strings.SplitAfter(string(b), "\n")
	target := sectionHeader(name)

	// One pass to locate the section.
	activeIdx, commentedFound := -1, false
	for i, line := range lines {
		leading := strings.TrimLeft(line, " \t")
		if rest, ok := strings.CutPrefix(leading, "#"); ok {
			if headerText(rest) == target {
				commentedFound = true
			}
			continue
		}
		if headerText(leading) == target {
			activeIdx = i
			break
		}
	}

	if activeIdx < 0 {
		if commentedFound {
			return true, nil
		}
		return false, fmt.Errorf("%w: %s", ErrAccountAbsent, name)
	}

	// Comment lines from activeIdx onward until the next non-matching header.
	for i := activeIdx; i < len(lines); i++ {
		line := lines[i]
		leading := strings.TrimLeft(line, " \t")

		if i > activeIdx && strings.HasPrefix(leading, "[") {
			if !headerBelongsTo(leading, name) {
				break
			}
		}

		if strings.HasPrefix(leading, "#") {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines[i] = "# " + line
	}

	if err := writeAtomic(path, []byte(strings.Join(lines, ""))); err != nil {
		return false, err
	}
	return false, nil
}

// headerBelongsTo reports whether the section header at line `leading` (with
// leading whitespace already trimmed) refers to the named account — either
// [accounts.<name>] exactly or [accounts.<name>.something].
func headerBelongsTo(leading, name string) bool {
	stripped := headerText(leading)
	if !strings.HasPrefix(stripped, "[") || !strings.HasSuffix(stripped, "]") {
		return false
	}
	inside := stripped[1 : len(stripped)-1]
	prefix := "accounts." + name
	return inside == prefix || strings.HasPrefix(inside, prefix+".")
}

// headerText trims surrounding whitespace and a TOML trailing `# comment`
// off a header-shaped line so `[accounts.x] # note` compares equal to
// `[accounts.x]`. `#` characters inside quoted segments don't occur in
// well-formed `[accounts.<name>]` headers (the name grammar excludes them),
// so a naive split on the first `#` outside `]` is safe.
func headerText(line string) string {
	s := strings.TrimSpace(line)
	if end := strings.Index(s, "]"); end >= 0 {
		if hash := strings.Index(s[end+1:], "#"); hash >= 0 {
			s = strings.TrimSpace(s[:end+1+hash])
		}
	}
	return s
}

// RenameAccount rewrites `[accounts.<old>]` (and any `[accounts.<old>.*]`
// sub-section headers) to use <new>, and inserts `aliases = ["<old>", ...]`
// into the renamed block so previously-emitted mbx IDs keep resolving
// (ADR-0007).
//
// Returns ErrAccountAbsent if `[accounts.<old>]` isn't in the file (active
// or commented), and ErrRenameTargetExists if `[accounts.<new>]` already
// appears. Does not touch external secret stores.
func RenameAccount(path, oldName, newName string) error {
	if oldName == newName {
		return fmt.Errorf("rename: old and new names are identical (%q)", oldName)
	}
	if exists, err := HasAccount(path, newName); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%w: %s", ErrRenameTargetExists, newName)
	}
	if exists, err := HasAccount(path, oldName); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("%w: %s", ErrAccountAbsent, oldName)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	lines := strings.SplitAfter(string(b), "\n")

	oldHeader := sectionHeader(oldName)
	newHeader := sectionHeader(newName)
	headerIdx, blockEnd := -1, len(lines)
	for i, line := range lines {
		leading := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(leading, "#") {
			continue
		}
		if headerText(leading) == oldHeader {
			lines[i] = newHeader + trailingAfterHeader(line, oldHeader)
			headerIdx = i
			continue
		}
		if headerIdx >= 0 && strings.HasPrefix(leading, "[") {
			if !headerBelongsTo(leading, oldName) {
				blockEnd = i
				break
			}
			// Sub-section header like [accounts.old.cache] → [accounts.new.cache].
			lines[i] = strings.Replace(line, "accounts."+oldName, "accounts."+newName, 1)
		}
	}
	if headerIdx < 0 {
		return fmt.Errorf("%w: %s", ErrAccountAbsent, oldName)
	}
	for i := headerIdx + 1; i < blockEnd; i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		if strings.HasPrefix(trimmed, "aliases") &&
			strings.HasPrefix(strings.TrimLeft(strings.TrimPrefix(trimmed, "aliases"), " \t"), "=") {
			return ErrRenameNeedsManualAliasMerge
		}
	}

	insertion := "aliases = [\"" + oldName + "\"]\n"
	lines = append(lines[:headerIdx+1], append([]string{insertion}, lines[headerIdx+1:]...)...)

	return writeAtomic(path, []byte(strings.Join(lines, "")))
}

// trailingAfterHeader returns whatever followed the `]` of `header` on its
// original line — typically just "\n", but possibly a trailing comment.
// Preserves user-written comments through a rename.
func trailingAfterHeader(line, header string) string {
	_, after, ok := strings.Cut(line, header)
	if !ok {
		return ""
	}
	return after
}

// AddTemplate appends block to the config file at path, registering the
// named account. Returns ErrAccountExists if `[accounts.<name>]` is already
// present in the file (commented or not) — never silently overwrites.
func AddTemplate(path, name, block string) error {
	present, err := HasAccount(path, name)
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("%w: %s", ErrAccountExists, name)
	}
	return appendBlock(path, block)
}

func appendBlock(path, block string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	var existing []byte
	if b, err := os.ReadFile(path); err == nil {
		existing = b
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	var buf strings.Builder
	if len(existing) == 0 {
		buf.WriteString(fileBanner)
	} else {
		buf.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			buf.WriteByte('\n')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString(block)
	if !strings.HasSuffix(block, "\n") {
		buf.WriteByte('\n')
	}

	return writeAtomic(path, []byte(buf.String()))
}

// writeAtomic writes data to path via a tempfile in the same directory plus
// rename, so a partial write can't corrupt an existing config.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.toml.*")
	if err != nil {
		return fmt.Errorf("creating tempfile: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing tempfile: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("renaming tempfile: %w", err)
	}
	return nil
}
