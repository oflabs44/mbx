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
