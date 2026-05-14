package message

import (
	"strings"
	"testing"
	"time"
)

func TestBuildReply_ThreadingHeaders(t *testing.T) {
	cases := []struct {
		name        string
		source      Message
		wantInReply string
		wantRefs    string
	}{
		{
			name: "canonical Message-Id key (matches what providers store)",
			source: Message{
				From:    "alice@example.com",
				Subject: "hi",
				Headers: map[string]string{
					"Message-Id":  "<orig@a.example>",
					"References":  "<root@a.example> <mid@a.example>",
					"In-Reply-To": "<mid@a.example>",
				},
			},
			wantInReply: "<orig@a.example>",
			wantRefs:    "<root@a.example> <mid@a.example> <orig@a.example>",
		},
		{
			name: "no References — In-Reply-To carries forward as the chain seed",
			source: Message{
				From:    "alice@example.com",
				Subject: "hi",
				Headers: map[string]string{
					"Message-Id":  "<orig@a.example>",
					"In-Reply-To": "<seed@a.example>",
				},
			},
			wantInReply: "<orig@a.example>",
			wantRefs:    "<seed@a.example> <orig@a.example>",
		},
		{
			name: "no Message-Id at all — reply ships without threading rather than refusing",
			source: Message{
				From:    "alice@example.com",
				Subject: "hi",
				Headers: map[string]string{},
			},
			wantInReply: "",
			wantRefs:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := BuildReply(tc.source, ReplyOpts{From: "me@example.com", Body: "ack"})
			if err != nil {
				t.Fatalf("BuildReply: %v", err)
			}
			if got := spec.Headers["In-Reply-To"]; got != tc.wantInReply {
				t.Errorf("In-Reply-To = %q, want %q", got, tc.wantInReply)
			}
			if got := spec.Headers["References"]; got != tc.wantRefs {
				t.Errorf("References = %q, want %q", got, tc.wantRefs)
			}
		})
	}
}

func TestBuildReply_RecipientsAndSubject(t *testing.T) {
	source := Message{
		From:    "alice@example.com",
		To:      []string{"me@example.com", "bob@example.com"},
		Cc:      []string{"carol@example.com"},
		Subject: "weekly sync",
		Headers: map[string]string{"Message-Id": "<x@example.com>"},
	}

	t.Run("default: To = source.From, Cc empty", func(t *testing.T) {
		spec, err := BuildReply(source, ReplyOpts{From: "me@example.com", Body: "ok"})
		if err != nil {
			t.Fatal(err)
		}
		if got := spec.To; len(got) != 1 || got[0] != "alice@example.com" {
			t.Errorf("To = %v, want [alice@example.com]", got)
		}
		if len(spec.Cc) != 0 {
			t.Errorf("Cc = %v, want empty", spec.Cc)
		}
	})

	t.Run("--all: Cc = source.To + source.Cc minus self", func(t *testing.T) {
		spec, err := BuildReply(source, ReplyOpts{From: "me@example.com", Body: "ok", All: true})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(spec.Cc, ",")
		if strings.Contains(joined, "me@example.com") {
			t.Errorf("Cc contains self: %v", spec.Cc)
		}
		if !strings.Contains(joined, "bob@example.com") || !strings.Contains(joined, "carol@example.com") {
			t.Errorf("Cc missing expected recipients: %v", spec.Cc)
		}
	})

	t.Run("Re: prefix is added once", func(t *testing.T) {
		spec, _ := BuildReply(source, ReplyOpts{From: "me@example.com", Body: "ok"})
		if spec.Subject != "Re: weekly sync" {
			t.Errorf("Subject = %q, want %q", spec.Subject, "Re: weekly sync")
		}
		alreadyPrefixed := source
		alreadyPrefixed.Subject = "Re: weekly sync"
		spec2, _ := BuildReply(alreadyPrefixed, ReplyOpts{From: "me@example.com", Body: "ok"})
		if spec2.Subject != "Re: weekly sync" {
			t.Errorf("re-prefixed Subject = %q, want %q", spec2.Subject, "Re: weekly sync")
		}
	})
}

func TestBuildForward(t *testing.T) {
	source := Message{
		From:    "alice@example.com",
		To:      []string{"me@example.com"},
		Subject: "report",
		Date:    time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC),
		Body:    "the report body",
		Headers: map[string]string{"Message-Id": "<r@example.com>"},
	}

	spec, err := BuildForward(source, ForwardOpts{
		From: "me@example.com",
		To:   []string{"colleague@example.com"},
		Body: "FYI",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Subject != "Fwd: report" {
		t.Errorf("Subject = %q, want %q", spec.Subject, "Fwd: report")
	}
	if len(spec.Headers) != 0 {
		t.Errorf("Headers should be empty on forward; got %v", spec.Headers)
	}
	if !strings.Contains(spec.Body, "FYI") {
		t.Errorf("Body missing user prose: %q", spec.Body)
	}
	if !strings.Contains(spec.Body, "the report body") {
		t.Errorf("Body missing forwarded source: %q", spec.Body)
	}
	if !strings.Contains(spec.Body, "---------- Forwarded message ----------") {
		t.Errorf("Body missing forward banner: %q", spec.Body)
	}
}
