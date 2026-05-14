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
		{"trailing comment", "[accounts.work] # hand-edited note\n", "work", true},
		{"trailing comment + leading hash", "# [accounts.work] # note\n", "work", true},
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

func TestRemoveAccount_Missing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("# nothing here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := RemoveAccount(path, "ghost")
	if !errors.Is(err, ErrAccountAbsent) {
		t.Fatalf("want ErrAccountAbsent, got %v", err)
	}
}

func TestRemoveAccount_MissingFile(t *testing.T) {
	_, err := RemoveAccount(filepath.Join(t.TempDir(), "does-not-exist.toml"), "x")
	if !errors.Is(err, ErrAccountAbsent) {
		t.Fatalf("want ErrAccountAbsent for missing file, got %v", err)
	}
}

func TestRemoveAccount_AlreadyCommented(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "# [accounts.work]\n# email = \"x\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	already, err := RemoveAccount(path, "work")
	if err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	if !already {
		t.Error("expected alreadyCommented=true for commented section")
	}

	// File must not be modified.
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Errorf("file was modified: %q", got)
	}
}

func TestRemoveAccount_CommentsActiveBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := `# header banner

[accounts.personal]
email = "you@gmail.com"
backend.type  = "gmail"
backend.login = "you@gmail.com"

[accounts.work]
email = "you@work.com"
backend.type = "imap"
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	already, err := RemoveAccount(path, "personal")
	if err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	if already {
		t.Error("expected alreadyCommented=false for active section")
	}

	got, _ := os.ReadFile(path)
	out := string(got)

	if !strings.Contains(out, "# [accounts.personal]") {
		t.Errorf("personal header not commented: %s", out)
	}
	if !strings.Contains(out, "# email = \"you@gmail.com\"") {
		t.Errorf("personal email not commented: %s", out)
	}
	if !strings.Contains(out, "\n[accounts.work]") {
		t.Errorf("work section was touched: %s", out)
	}
	if !strings.Contains(out, "\nemail = \"you@work.com\"") {
		t.Errorf("work email was commented: %s", out)
	}
}

func TestRemoveAccount_StopsAtNextAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := `[accounts.a]
backend.type = "gmail"
[accounts.b]
backend.type = "imap"
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveAccount(path, "a"); err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	got, _ := os.ReadFile(path)
	want := `# [accounts.a]
# backend.type = "gmail"
[accounts.b]
backend.type = "imap"
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRemoveAccount_KeepsSubsectionsInBlock(t *testing.T) {
	// Hand-written legacy shape: account has a [accounts.<name>.cache]
	// sub-section. Removing the account should comment the sub-section too.
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := `[accounts.work]
email = "x@y.z"
[accounts.work.cache]
sync_days = 30
[accounts.other]
email = "o@y.z"
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveAccount(path, "work"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	out := string(got)
	if !strings.Contains(out, "# [accounts.work]") {
		t.Errorf("work header not commented: %s", out)
	}
	if !strings.Contains(out, "# [accounts.work.cache]") {
		t.Errorf("work.cache sub-section not commented: %s", out)
	}
	if !strings.Contains(out, "# sync_days = 30") {
		t.Errorf("work.cache contents not commented: %s", out)
	}
	if !strings.Contains(out, "\n[accounts.other]") {
		t.Errorf("other account was touched: %s", out)
	}
}

func TestRemoveAccount_HeaderWithTrailingComment(t *testing.T) {
	// TOML allows a trailing `# comment` on a header line. The locator
	// must still recognise the section, otherwise removal silently
	// reports ErrAccountAbsent for a valid active account.
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := "[accounts.work]   # primary\nemail = \"x\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	already, err := RemoveAccount(path, "work")
	if err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	if already {
		t.Error("expected alreadyCommented=false")
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "# [accounts.work]") {
		t.Errorf("header not commented: %s", got)
	}
}

func TestRemoveAccount_Atomic_NoCorruptionOnFailure(t *testing.T) {
	// writeAtomic writes via tempfile + rename. We can't easily inject a
	// failure, but verify the file isn't truncated when no failure occurs.
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := "[accounts.x]\nemail = \"a\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveAccount(path, "x"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Error("file was truncated")
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

func TestRenameAccount_HappyPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	content := "[accounts.personal]\nemail = \"x@y\"\nbackend.type = \"gmail\"\n\n[accounts.work]\nemail = \"z@w\"\nbackend.type = \"imap\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenameAccount(path, "personal", "personal-gmail"); err != nil {
		t.Fatalf("RenameAccount: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gotS := string(got)
	if !strings.Contains(gotS, "[accounts.personal-gmail]") {
		t.Errorf("new header missing:\n%s", gotS)
	}
	if strings.Contains(gotS, "[accounts.personal]\n") {
		t.Errorf("old header still active:\n%s", gotS)
	}
	if !strings.Contains(gotS, "aliases = [\"personal\"]") {
		t.Errorf("alias line missing:\n%s", gotS)
	}
	if !strings.Contains(gotS, "[accounts.work]") {
		t.Errorf("untouched account [accounts.work] should remain:\n%s", gotS)
	}
}

func TestRenameAccount_RenamesSubSections(t *testing.T) {
	// [accounts.old.cache] sub-sections must follow the parent rename.
	path := filepath.Join(t.TempDir(), "c.toml")
	content := "[accounts.old]\nemail = \"x@y\"\nbackend.type = \"imap\"\n\n[accounts.old.cache]\nsync_days = 30\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenameAccount(path, "old", "new"); err != nil {
		t.Fatalf("RenameAccount: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "[accounts.new.cache]") {
		t.Errorf("sub-section not renamed:\n%s", got)
	}
}

func TestRenameAccount_TargetExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	content := "[accounts.a]\nemail = \"x@y\"\n\n[accounts.b]\nemail = \"z@w\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	err := RenameAccount(path, "a", "b")
	if !errors.Is(err, ErrRenameTargetExists) {
		t.Errorf("want ErrRenameTargetExists, got %v", err)
	}
}

func TestRenameAccount_SourceAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte("[accounts.a]\nemail = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := RenameAccount(path, "missing", "new")
	if !errors.Is(err, ErrAccountAbsent) {
		t.Errorf("want ErrAccountAbsent, got %v", err)
	}
}

func TestRenameAccount_ExistingAliasesRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	content := "[accounts.old]\nemail = \"x\"\naliases = [\"older\"]\nbackend.type = \"gmail\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	err := RenameAccount(path, "old", "new")
	if !errors.Is(err, ErrRenameNeedsManualAliasMerge) {
		t.Errorf("want ErrRenameNeedsManualAliasMerge, got %v", err)
	}
	// File should be unchanged on refusal.
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Errorf("file mutated on refusal:\n%s", got)
	}
}
