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
	c.Stdin = stdin
	var out, errBuf bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errBuf
	if err := c.Run(); err != nil {
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
