package main

import (
	"io"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/oflabs44/mbx/internal/output"
)

// Overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

type versionData struct {
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	Commit    string `json:"commit,omitempty"`
}

func newVersionCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print mbx version and build info",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			goVer, commit := buildInfo()
			return output.NewWriter(stdout, stderr, g.format()).Success(versionData{
				Version:   version,
				GoVersion: goVer,
				Commit:    commit,
			}, nil)
		},
	}
}

func buildInfo() (goVersion, commit string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			commit = s.Value
			break
		}
	}
	return info.GoVersion, commit
}
