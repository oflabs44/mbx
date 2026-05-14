package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/oflabs44/mbx/internal/attachment"
	"github.com/oflabs44/mbx/internal/mbxid"
	"github.com/oflabs44/mbx/internal/output"
)

func newAttachmentCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attachment",
		Short: "List and download message attachments",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newAttachmentListCmd(g, stdout, stderr),
		newAttachmentDownloadCmd(g, stdout, stderr),
	)
	return cmd
}

func newAttachmentListCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list <message-id>",
		Short: "List attachments on a message without downloading",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			msgID, err := mbxid.Parse(args[0])
			if err != nil {
				return output.Errorf(output.CodeUsageInvalid, "parsing id: %s", err.Error())
			}
			return runAttachmentList(cmd.Context(), g, stdout, stderr, msgID)
		},
	}
}

func newAttachmentDownloadCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	var outPath string
	c := &cobra.Command{
		Use:   "download <attachment-id>",
		Short: "Download an attachment to disk (or to stdout with -o -)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			msgID, index, err := attachment.SplitID(args[0])
			if err != nil {
				return output.Errorf(output.CodeUsageInvalid, "%s", err.Error())
			}
			return runAttachmentDownload(cmd.Context(), g, stdout, stderr, msgID, index, outPath)
		},
	}
	// -o is the global --output flag's short letter, which would conflict.
	// Use --out for the destination path; document the divergence in help.
	c.Flags().StringVar(&outPath, "out", "", "Destination directory or file path. Default: $XDG_DOWNLOAD_DIR or CWD. Use '-' for stdout.")
	return c
}

func runAttachmentList(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, msgID mbxid.ID) error {
	acct, err := lookupAccountForID(g, msgID)
	if err != nil {
		return err
	}
	backend, err := newBackend(ctx, msgID.Account, acct)
	if err != nil {
		return err
	}
	metas, err := backend.ListAttachments(ctx, msgID)
	if err != nil {
		return err
	}
	return output.NewWriter(stdout, stderr, g.format()).Success(metas, nil)
}

func runAttachmentDownload(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, msgID mbxid.ID, index int, outPath string) error {
	acct, err := lookupAccountForID(g, msgID)
	if err != nil {
		return err
	}
	backend, err := newBackend(ctx, msgID.Account, acct)
	if err != nil {
		return err
	}
	data, err := backend.DownloadAttachment(ctx, msgID, index)
	if err != nil {
		return err
	}

	if outPath == "-" {
		_, err := stdout.Write(data.Bytes)
		return err
	}

	dest, err := resolveAttachmentDest(outPath, data.Filename)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest, data.Bytes, 0o644); err != nil {
		return fmt.Errorf("writing attachment to %s: %w", dest, err)
	}
	type res struct {
		Path     string `json:"path"`
		Filename string `json:"filename"`
		Size     int    `json:"size"`
		MIME     string `json:"mime"`
	}
	return output.NewWriter(stdout, stderr, g.format()).Success(res{
		Path:     dest,
		Filename: data.Filename,
		Size:     len(data.Bytes),
		MIME:     data.MIME,
	}, nil)
}

// resolveAttachmentDest follows the documented precedence: explicit path
// (file or dir) > $XDG_DOWNLOAD_DIR > CWD. When the resolved location is
// a directory, the attachment's own filename is appended.
func resolveAttachmentDest(outPath, attachmentName string) (string, error) {
	chosen := outPath
	if chosen == "" {
		if x := os.Getenv("XDG_DOWNLOAD_DIR"); x != "" {
			chosen = x
		} else {
			chosen = "."
		}
	}
	info, err := os.Stat(chosen)
	if err == nil && info.IsDir() {
		if attachmentName == "" {
			return "", fmt.Errorf("output is a directory but attachment has no filename; pass --out <file>")
		}
		return filepath.Join(chosen, attachmentName), nil
	}
	return chosen, nil
}
