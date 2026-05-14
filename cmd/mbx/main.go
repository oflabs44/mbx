package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/oflabs44/mbx/internal/output"
)

func main() {
	os.Exit(run(os.Stdout, os.Stderr, os.Args[1:]))
}

func run(stdout, stderr io.Writer, args []string) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	root, g := newRootCmd(stdout, stderr)
	root.SetArgs(args)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}

	f := output.AsFailure(err)
	_ = output.NewWriter(stdout, stderr, g.format()).Failure(f)
	return output.ExitCode(f.Code)
}
