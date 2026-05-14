package gmail

import (
	"reflect"
	"testing"
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
