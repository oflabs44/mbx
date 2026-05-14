package secret

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oflabs44/mbx/internal/config"
)

// skipOnWindows skips tests whose shell command assumes a POSIX `sh -c` host.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
}

func TestRead_Raw(t *testing.T) {
	got, err := Read(context.Background(), &config.Secret{Raw: "hunter2"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("got %q, want %q", got, "hunter2")
	}
}

func TestRead_Cmd(t *testing.T) {
	skipOnWindows(t)
	got, err := Read(context.Background(), &config.Secret{Cmd: "printf 'tok-from-cmd'"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "tok-from-cmd" {
		t.Errorf("got %q, want %q", got, "tok-from-cmd")
	}
}

func TestRead_CmdTrimsTrailingNewline(t *testing.T) {
	skipOnWindows(t)
	got, err := Read(context.Background(), &config.Secret{Cmd: "echo with-trailing-newline"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "with-trailing-newline" {
		t.Errorf("got %q, want %q", got, "with-trailing-newline")
	}
}

func TestRead_CmdFailurePropagatesStderr(t *testing.T) {
	skipOnWindows(t)
	_, err := Read(context.Background(), &config.Secret{Cmd: "echo boom 1>&2; exit 7"})
	if !errors.Is(err, ErrCmdFailed) {
		t.Fatalf("err = %v, want ErrCmdFailed", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error message missing stderr content: %v", err)
	}
}

func TestRead_NoVariant(t *testing.T) {
	_, err := Read(context.Background(), &config.Secret{})
	if !errors.Is(err, ErrNoVariant) {
		t.Fatalf("err = %v, want ErrNoVariant", err)
	}
}

func TestRead_NilSecret(t *testing.T) {
	_, err := Read(context.Background(), nil)
	if !errors.Is(err, ErrNoVariant) {
		t.Fatalf("err = %v, want ErrNoVariant", err)
	}
}

func TestWrite_PipesValueToWriteCmd(t *testing.T) {
	skipOnWindows(t)
	target := filepath.Join(t.TempDir(), "out")
	s := &config.Secret{WriteCmd: "cat > " + target}
	if err := Write(context.Background(), s, "rotated-token-value"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "rotated-token-value" {
		t.Errorf("file contents = %q, want %q", got, "rotated-token-value")
	}
}

func TestWrite_NoWriteCmd(t *testing.T) {
	err := Write(context.Background(), &config.Secret{Raw: "x"}, "anything")
	if !errors.Is(err, ErrNoWriteCmd) {
		t.Fatalf("err = %v, want ErrNoWriteCmd", err)
	}
}

func TestRead_Keyring_SkipsWithoutBackend(t *testing.T) {
	// The keyring path depends on a usable OS keyring; CI environments
	// generally don't provide one. A live keyring round-trip is exercised
	// manually. Either of the two sentinel errors is acceptable here —
	// ErrKeyringNotFound (backend present, item missing) or ErrKeyringRead
	// (backend unreachable / locked).
	_, err := Read(context.Background(), &config.Secret{Keyring: "mbx-test-nonexistent-item-zzz"})
	if err == nil {
		t.Skip("keyring backend available and item happens to exist; skipping negative-path assertion")
	}
	if !errors.Is(err, ErrKeyringRead) && !errors.Is(err, ErrKeyringNotFound) {
		t.Fatalf("err = %v, want ErrKeyringRead or ErrKeyringNotFound", err)
	}
}

// fileBackedSecret wires a Secret to a tempfile so Read and Write actually
// share state — the right setup for round-trip preflight tests.
func fileBackedSecret(t *testing.T) (*config.Secret, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	return &config.Secret{
		Cmd:      "cat " + path,
		WriteCmd: "cat > " + path,
	}, path
}

func TestPreflight_RoundTripsExistingValue(t *testing.T) {
	skipOnWindows(t)
	s, path := fileBackedSecret(t)
	if err := os.WriteFile(path, []byte("existing-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Preflight(context.Background(), s); err != nil {
		t.Fatalf("Preflight: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing-token" {
		t.Errorf("preflight altered the existing value: got %q", got)
	}
}

func TestPreflight_SentinelWhenNoExistingValue(t *testing.T) {
	skipOnWindows(t)
	s, path := fileBackedSecret(t)
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Preflight(context.Background(), s); err != nil {
		t.Fatalf("Preflight: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "MBX-PREFLIGHT-") {
		t.Errorf("sentinel not written; file contains %q", got)
	}
}

func TestPreflight_DetectsWriteCmdFailure(t *testing.T) {
	skipOnWindows(t)
	s := &config.Secret{
		Cmd:      "echo current",
		WriteCmd: "false", // always exits 1
	}
	err := Preflight(context.Background(), s)
	if err == nil {
		t.Fatal("want preflight error, got nil")
	}
	if !errors.Is(err, ErrCmdFailed) {
		t.Errorf("err = %v, want wrap of ErrCmdFailed", err)
	}
}

func TestPreflight_NoWriteCmd(t *testing.T) {
	err := Preflight(context.Background(), &config.Secret{Raw: "x"})
	if !errors.Is(err, ErrNoWriteCmd) {
		t.Fatalf("err = %v, want ErrNoWriteCmd", err)
	}
}

func TestRead_CmdTimeoutIsDistinguishable(t *testing.T) {
	skipOnWindows(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Read(ctx, &config.Secret{Cmd: "sleep 1"})
	if err == nil {
		t.Fatal("expected error from timed-out command")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want errors.Is(..., context.DeadlineExceeded)", err)
	}
}
