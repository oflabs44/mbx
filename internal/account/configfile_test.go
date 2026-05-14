package account

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasAccount_MissingFile(t *testing.T) {
	got, err := HasAccount(filepath.Join(t.TempDir(), "nope.toml"), "work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("want false for missing file")
	}
}

func TestHasAccount_Detection(t *testing.T) {
	cases := []struct {
		name    string
		content string
		acct    string
		want    bool
	}{
		{"uncommented", "[accounts.work]\ntype = \"imap\"\n", "work", true},
		{"commented hash", "# [accounts.work]\n", "work", true},
		{"commented hash space", "  #   [accounts.work]\n", "work", true},
		{"different name", "[accounts.work]\n", "play", false},
		{"prefix not match", "[accounts.workspace]\n", "work", false},
		{"empty file", "", "work", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.toml")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := HasAccount(path, tc.acct)
			if err != nil {
				t.Fatalf("HasAccount: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
		})
	}
}

func TestAddTemplate_CreatesFileWithBanner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.toml")

	if err := AddTemplate(path, "work", GmailTemplate("work")); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(b)
	if !strings.HasPrefix(got, "# mbx config") {
		t.Errorf("expected banner at top, got: %q", got[:min(60, len(got))])
	}
	if !strings.Contains(got, "[accounts.work]") {
		t.Errorf("expected section header for work, got: %s", got)
	}
}

func TestAddTemplate_AppendsWithSeparator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := "[accounts.first]\ntype = \"gmail\"\nemail = \"a@b\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := AddTemplate(path, "second", IMAPTemplate("second")); err != nil {
		t.Fatalf("AddTemplate: %v", err)
	}

	b, _ := os.ReadFile(path)
	got := string(b)
	if !strings.HasPrefix(got, initial) {
		t.Fatalf("existing content not preserved at head, got: %s", got)
	}
	if !strings.Contains(got, "[accounts.second]") {
		t.Fatalf("appended block missing, got: %s", got)
	}
	// Existing content and the new block should be separated by a blank
	// line (template itself opens with a `# Fill in...` comment).
	if !strings.Contains(got, "\n\n# Fill in") {
		t.Errorf("expected blank-line separator before appended block, got: %s", got)
	}
}

func TestAddTemplate_RefusesDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[accounts.work]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := AddTemplate(path, "work", GmailTemplate("work"))
	if !errors.Is(err, ErrAccountExists) {
		t.Fatalf("want ErrAccountExists, got %v", err)
	}
}

func TestAddTemplate_RefusesCommentedDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("# [accounts.work]\n# type = \"imap\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := AddTemplate(path, "work", GmailTemplate("work"))
	if !errors.Is(err, ErrAccountExists) {
		t.Fatalf("want ErrAccountExists for commented section, got %v", err)
	}
}

func TestTemplates_SubstituteName(t *testing.T) {
	got := GmailTemplate("personal")
	if strings.Contains(got, "{{name}}") {
		t.Errorf("gmail template still contains {{name}}: %s", got)
	}
	if !strings.Contains(got, "[accounts.personal]") {
		t.Errorf("gmail template missing substituted header: %s", got)
	}

	got = IMAPTemplate("work")
	if strings.Contains(got, "{{name}}") {
		t.Errorf("imap template still contains {{name}}: %s", got)
	}
	if !strings.Contains(got, "[accounts.work]") {
		t.Errorf("imap template missing substituted header: %s", got)
	}
}
