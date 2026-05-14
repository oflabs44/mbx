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
		if strings.TrimSpace(stripComment(sc.Text())) == target {
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
