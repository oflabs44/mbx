// Package secret resolves a config.Secret to a value (Read) and persists
// rotated values via write_cmd (Write). See ADR-0001 for the resolution
// model. Three read variants: raw (inline), keyring (OS keychain via
// zalando/go-keyring), cmd (any shell command — stdout is the value).
// Writes always go through write_cmd; the OS keyring is not a built-in
// write target — users who want keyring-backed persistence configure a
// write_cmd like `security add-generic-password ...`.
package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/oflabs44/mbx/internal/config"
)

var (
	ErrNoVariant       = errors.New("secret has no read variant set")
	ErrNoWriteCmd      = errors.New("secret has no write_cmd")
	ErrCmdFailed       = errors.New("secret command failed")
	ErrKeyringRead     = errors.New("keyring read failed")
	ErrKeyringNotFound = errors.New("keyring item not found")
)

// Debug is invoked once per shell-out from secret commands when the caller
// has opted into debug logging (`--debug` in cmd/mbx). The default is a
// no-op; set this var from main once at startup. The format is "secret: ..."
// printed on the writer the caller chooses (typically stderr). Never logs
// the secret value itself — only the rendered shell command (which may
// contain `$(cat)` placeholders), stdin byte count, exit status, and full
// stderr from the spawned process.
var Debug func(format string, args ...any) = func(string, ...any) {}

// Mbx uses a single keyring user across all secrets; the service name comes
// from the user's config (e.g. `keyring = "mbx-gmail-refresh-token"`).
const keyringUser = "mbx"

func Read(ctx context.Context, s *config.Secret) (string, error) {
	if s == nil {
		return "", ErrNoVariant
	}
	v, val := s.Variant()
	switch v {
	case config.SecretRaw:
		return val, nil
	case config.SecretKeyring:
		pwd, err := keyring.Get(val, keyringUser)
		if err != nil {
			if errors.Is(err, keyring.ErrNotFound) {
				return "", fmt.Errorf("%w (%s)", ErrKeyringNotFound, val)
			}
			return "", fmt.Errorf("%w (%s): %v", ErrKeyringRead, val, err)
		}
		return pwd, nil
	case config.SecretCmd:
		return runCmd(ctx, val, nil)
	default:
		return "", ErrNoVariant
	}
}

func Write(ctx context.Context, s *config.Secret, value string) error {
	if s == nil || s.WriteCmd == "" {
		return ErrNoWriteCmd
	}
	_, err := runCmd(ctx, s.WriteCmd, strings.NewReader(value))

	return err
}

func runCmd(ctx context.Context, cmd string, stdin io.Reader) (string, error) {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd.exe", "/c", cmd)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", cmd)
	}

	// Tee stdin so we can count bytes for debug without buffering the whole
	// secret in memory twice. The counter wrapper is byte-only; the secret
	// value itself never lands in the log.
	var stdinBytes int64
	if stdin != nil {
		stdin = &countingReader{r: stdin, n: &stdinBytes}
	}
	c.Stdin = stdin

	var out, errBuf bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errBuf

	Debug("run (sh -c): %s", cmd)
	err := c.Run()
	Debug("run done: exit=%v stdin_bytes=%d stdout_bytes=%d stderr=%q",
		exitStatus(err), stdinBytes, out.Len(), strings.TrimSpace(errBuf.String()))

	if err != nil {
		// Distinguish cancellation/deadline from a genuine command failure so
		// callers can errors.Is(err, context.DeadlineExceeded) and behave
		// differently (retry with longer timeout vs surface to user).
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("%w: %w", ctxErr, err)
		}
		stderr := strings.TrimSpace(errBuf.String())
		if stderr == "" {
			return "", fmt.Errorf("%w: %v", ErrCmdFailed, err)
		}
		return "", fmt.Errorf("%w: %v: %s", ErrCmdFailed, err, stderr)
	}
	// Convention: CLI secret tools (op, pass, security) emit a single trailing
	// newline. Trim exactly one — preserving any embedded newlines a caller
	// might legitimately want in a multi-line secret.
	return strings.TrimSuffix(out.String(), "\n"), nil
}

// countingReader counts bytes read; used so Debug can report stdin size
// without buffering the secret value.
type countingReader struct {
	r io.Reader
	n *int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += int64(n)
	return n, err
}

// exitStatus turns an exec.Cmd error into a number suitable for logging.
// nil → 0; *exec.ExitError → its code; anything else → -1 (failed to spawn).
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	return -1
}
