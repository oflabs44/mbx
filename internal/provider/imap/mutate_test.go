package imap

import (
	"errors"
	"testing"

	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/output"
)

func TestResolveRoleFolder(t *testing.T) {
	cases := []struct {
		name     string
		folder   *config.Folder
		role     string
		wantPath string
		wantErr  bool
	}{
		{
			name:     "alias present",
			folder:   &config.Folder{Aliases: map[string]string{"archive": "Archive"}},
			role:     "archive",
			wantPath: "Archive",
		},
		{
			name:    "alias empty string treated as unset",
			folder:  &config.Folder{Aliases: map[string]string{"archive": ""}},
			role:    "archive",
			wantErr: true,
		},
		{
			name:    "folder block nil",
			folder:  nil,
			role:    "archive",
			wantErr: true,
		},
		{
			name:    "role missing from map",
			folder:  &config.Folder{Aliases: map[string]string{"trash": "Trash"}},
			role:    "archive",
			wantErr: true,
		},
		{
			name:     "trash role resolves the same way",
			folder:   &config.Folder{Aliases: map[string]string{"trash": "[Gmail]/Trash"}},
			role:     "trash",
			wantPath: "[Gmail]/Trash",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{Account: "test", Cfg: &config.Account{Folder: tc.folder}}
			got, err := c.resolveRoleFolder(tc.role, "`message archive`")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveRoleFolder(%q) = %q, want error", tc.role, got)
				}
				var f *output.Failure
				if !errors.As(err, &f) || f.Code != output.CodeConfigInvalid {
					t.Fatalf("resolveRoleFolder(%q) err = %v, want CodeConfigInvalid", tc.role, err)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveRoleFolder(%q) unexpected err: %v", tc.role, err)
			}
			if got != tc.wantPath {
				t.Fatalf("resolveRoleFolder(%q) = %q, want %q", tc.role, got, tc.wantPath)
			}
		})
	}
}
