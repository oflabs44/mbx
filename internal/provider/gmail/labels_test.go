package gmail

import (
	"errors"
	"reflect"
	"testing"

	gmailv1 "google.golang.org/api/gmail/v1"

	"github.com/oflabs44/mbx/internal/output"
)

func TestFlagsFromLabels(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty (no UNREAD → seen)", nil, []string{"seen"}},
		{"UNREAD present → no seen", []string{"INBOX", "UNREAD"}, []string{}},
		{"starred + draft (read)", []string{"STARRED", "DRAFT"}, []string{"flagged", "draft", "seen"}},
		{"starred + draft (unread)", []string{"STARRED", "DRAFT", "UNREAD"}, []string{"flagged", "draft"}},
		{"important is dropped", []string{"INBOX", "IMPORTANT"}, []string{"seen"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := flagsFromLabels(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("flagsFromLabels(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestFoldersFromLabels(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"plain inbox", []string{"INBOX"}, []string{"INBOX"}},
		{"strips unread + starred + important", []string{"INBOX", "UNREAD", "STARRED", "IMPORTANT"}, []string{"INBOX"}},
		{"strips category tabs", []string{"INBOX", "CATEGORY_PROMOTIONS", "CATEGORY_SOCIAL"}, []string{"INBOX"}},
		{"draft is also a folder", []string{"DRAFT"}, []string{"DRAFT"}},
		{"user labels pass through", []string{"INBOX", "Label_42"}, []string{"INBOX", "Label_42"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := foldersFromLabels(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("foldersFromLabels(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestResolveLabelID(t *testing.T) {
	labels := []*gmailv1.Label{
		{Id: "INBOX", Name: "INBOX"},
		{Id: "TRASH", Name: "TRASH"},
		{Id: "Label_42", Name: "Newsletters"},
		{Id: "Label_99", Name: "Receipts/2026"},
	}

	cases := []struct {
		name    string
		in      string
		wantID  string
		wantErr bool
	}{
		{"system label resolves as itself", "INBOX", "INBOX", false},
		{"system trash resolves as itself", "TRASH", "TRASH", false},
		{"user label resolves to opaque id", "Newsletters", "Label_42", false},
		{"user label with slashes", "Receipts/2026", "Label_99", false},
		{"unknown name errors as not_found", "Archive", "", true},
		{"empty name errors as not_found", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveLabelID(labels, tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveLabelID(%q) = %q, want error", tc.in, got)
				}
				var f *output.Failure
				if !errors.As(err, &f) || f.Code != output.CodeProviderNotFound {
					t.Fatalf("resolveLabelID(%q) err = %v, want CodeProviderNotFound failure", tc.in, err)
				}

				return
			}
			if err != nil {
				t.Fatalf("resolveLabelID(%q) unexpected err: %v", tc.in, err)
			}
			if got != tc.wantID {
				t.Fatalf("resolveLabelID(%q) = %q, want %q", tc.in, got, tc.wantID)
			}
		})
	}
}
