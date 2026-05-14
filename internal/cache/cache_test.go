package cache

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/mbxid"
)

// nopResolver passes account names through unchanged. Used by tests
// where alias resolution isn't under test.
type nopResolver struct{}

func (nopResolver) Resolve(name string) (string, bool) { return name, true }

func tmpStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func sampleEnvelope(account, msgID string, date time.Time, flags, folders []string) envelope.Envelope {
	return envelope.Envelope{
		ID:       mbxid.NewGmail(account, msgID).String(),
		Account:  account,
		From:     "alice@example.com",
		To:       []string{"bob@example.com"},
		Subject:  "Hello " + msgID,
		Date:     date,
		Flags:    flags,
		Folders:  folders,
		Snippet:  "Hi — body of " + msgID,
		Provider: "gmail",
	}
}

func TestPutAndList(t *testing.T) {
	st := tmpStore(t)
	ctx := context.Background()
	r := nopResolver{}

	base := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	envs := []envelope.Envelope{
		sampleEnvelope("work", "m1", base, nil, []string{"INBOX"}),
		sampleEnvelope("work", "m2", base.Add(time.Hour), []string{"seen"}, []string{"INBOX"}),
		sampleEnvelope("work", "m3", base.Add(2*time.Hour), []string{"seen", "flagged"}, []string{"INBOX"}),
	}
	if err := st.PutEnvelopes(ctx, "work", envs, r); err != nil {
		t.Fatalf("PutEnvelopes: %v", err)
	}

	page, err := st.ListEnvelopes(ctx, ListQuery{
		ListQuery: envelope.ListQuery{Limit: 10},
		Accounts:  []string{"work"},
	}, r)
	if err != nil {
		t.Fatalf("ListEnvelopes: %v", err)
	}
	if got, want := len(page.Envelopes), 3; got != want {
		t.Fatalf("got %d envelopes, want %d", got, want)
	}
	// Newest first.
	if !strings.HasSuffix(page.Envelopes[0].ID, ":m3") {
		t.Errorf("first envelope id = %q, want m3", page.Envelopes[0].ID)
	}
	if got := page.Envelopes[0].Flags; len(got) != 2 {
		t.Errorf("m3 flags = %v, want 2 entries", got)
	}
}

func TestListFilters_UnreadStarred(t *testing.T) {
	st := tmpStore(t)
	ctx := context.Background()
	r := nopResolver{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	envs := []envelope.Envelope{
		sampleEnvelope("work", "u", base, nil, []string{"INBOX"}), // unread
		sampleEnvelope("work", "r", base.Add(time.Hour), []string{"seen"}, []string{"INBOX"}),
		sampleEnvelope("work", "s", base.Add(2*time.Hour), []string{"flagged"}, []string{"INBOX"}),
	}
	if err := st.PutEnvelopes(ctx, "work", envs, r); err != nil {
		t.Fatalf("Put: %v", err)
	}

	unread := true
	page, err := st.ListEnvelopes(ctx, ListQuery{
		ListQuery: envelope.ListQuery{Unread: &unread, Limit: 10},
		Accounts:  []string{"work"},
	}, r)
	if err != nil {
		t.Fatalf("List unread: %v", err)
	}
	if len(page.Envelopes) != 2 {
		t.Errorf("unread count = %d, want 2", len(page.Envelopes))
	}

	starred := true
	page, err = st.ListEnvelopes(ctx, ListQuery{
		ListQuery: envelope.ListQuery{Starred: &starred, Limit: 10},
		Accounts:  []string{"work"},
	}, r)
	if err != nil {
		t.Fatalf("List starred: %v", err)
	}
	if len(page.Envelopes) != 1 {
		t.Errorf("starred count = %d, want 1", len(page.Envelopes))
	}
}

func TestUpdateFlags(t *testing.T) {
	st := tmpStore(t)
	ctx := context.Background()
	r := nopResolver{}
	env := sampleEnvelope("work", "x1", time.Now().UTC(), nil, []string{"INBOX"})
	if err := st.PutEnvelopes(ctx, "work", []envelope.Envelope{env}, r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := st.UpdateFlags(ctx, env.ID, []envelope.Flag{envelope.FlagSeen}, nil, r); err != nil {
		t.Fatalf("UpdateFlags add: %v", err)
	}
	read := false
	page, _ := st.ListEnvelopes(ctx, ListQuery{
		ListQuery: envelope.ListQuery{Unread: &read, Limit: 10},
		Accounts:  []string{"work"},
	}, r)
	if len(page.Envelopes) != 1 {
		t.Fatalf("expected 1 read envelope after flag add, got %d", len(page.Envelopes))
	}
	if err := st.UpdateFlags(ctx, env.ID, nil, []envelope.Flag{envelope.FlagSeen}, r); err != nil {
		t.Fatalf("UpdateFlags remove: %v", err)
	}
	unread := true
	page, _ = st.ListEnvelopes(ctx, ListQuery{
		ListQuery: envelope.ListQuery{Unread: &unread, Limit: 10},
		Accounts:  []string{"work"},
	}, r)
	if len(page.Envelopes) != 1 {
		t.Fatalf("expected unread after flag removal, got %d", len(page.Envelopes))
	}
}

func TestDeleteByIDs(t *testing.T) {
	st := tmpStore(t)
	ctx := context.Background()
	r := nopResolver{}
	env := sampleEnvelope("work", "d1", time.Now().UTC(), nil, []string{"INBOX"})
	if err := st.PutEnvelopes(ctx, "work", []envelope.Envelope{env}, r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := st.DeleteByIDs(ctx, []string{env.ID}, r); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	page, _ := st.ListEnvelopes(ctx, ListQuery{
		ListQuery: envelope.ListQuery{Limit: 10},
		Accounts:  []string{"work"},
	}, r)
	if len(page.Envelopes) != 0 {
		t.Errorf("expected 0 envelopes after delete, got %d", len(page.Envelopes))
	}
}

func TestStatusAndSyncState(t *testing.T) {
	st := tmpStore(t)
	ctx := context.Background()
	r := nopResolver{}
	env := sampleEnvelope("work", "s1", time.Now().UTC(), nil, []string{"INBOX"})
	if err := st.PutEnvelopes(ctx, "work", []envelope.Envelope{env}, r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := st.UpsertSyncState(ctx, "work", "INBOX", 12345, 1, r); err != nil {
		t.Fatalf("UpsertSyncState: %v", err)
	}
	rows, err := st.Status(ctx, []string{"work"}, r)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(rows) != 1 || rows[0].Account != "work" || rows[0].Rows != 1 {
		t.Errorf("status = %+v", rows)
	}
	if rows[0].LastSyncAt.IsZero() {
		t.Errorf("expected non-zero LastSyncAt")
	}
}

func TestClearAccount(t *testing.T) {
	st := tmpStore(t)
	ctx := context.Background()
	r := nopResolver{}
	if err := st.PutEnvelopes(ctx, "work", []envelope.Envelope{
		sampleEnvelope("work", "c1", time.Now().UTC(), nil, []string{"INBOX"}),
	}, r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := st.UpsertSyncState(ctx, "work", "INBOX", 0, 1, r); err != nil {
		t.Fatalf("UpsertSyncState: %v", err)
	}
	if err := st.ClearAccount(ctx, "work", r); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	rows, _ := st.Status(ctx, nil, r)
	if len(rows) != 0 {
		t.Errorf("expected no rows after clear, got %+v", rows)
	}
}

// aliasingResolver maps "old" → "new"; everything else identity.
type aliasingResolver struct{}

func (aliasingResolver) Resolve(name string) (string, bool) {
	if name == "old" {
		return "new", true
	}
	return name, true
}

func TestCanonicalizeOnWrite(t *testing.T) {
	st := tmpStore(t)
	ctx := context.Background()
	// Build an envelope keyed under "old"; the resolver should canonicalize
	// it to "new" before it lands in the store.
	env := sampleEnvelope("old", "alias-1", time.Now().UTC(), nil, []string{"INBOX"})
	if err := st.PutEnvelopes(ctx, "old", []envelope.Envelope{env}, aliasingResolver{}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rows, _ := st.Status(ctx, nil, nopResolver{})
	if len(rows) != 1 || rows[0].Account != "new" {
		t.Fatalf("expected single row under 'new', got %+v", rows)
	}
}

func TestRenameAccount(t *testing.T) {
	st := tmpStore(t)
	ctx := context.Background()
	r := nopResolver{}
	env := sampleEnvelope("old", "r1", time.Now().UTC(), []string{"seen"}, []string{"INBOX"})
	if err := st.PutEnvelopes(ctx, "old", []envelope.Envelope{env}, r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := st.UpsertSyncState(ctx, "old", "INBOX", 0, 1, r); err != nil {
		t.Fatalf("UpsertSyncState: %v", err)
	}
	if err := st.RenameAccount(ctx, "old", "new"); err != nil {
		t.Fatalf("RenameAccount: %v", err)
	}
	rows, _ := st.Status(ctx, nil, r)
	if len(rows) != 1 || rows[0].Account != "new" || rows[0].Rows != 1 {
		t.Fatalf("after rename, status = %+v", rows)
	}
	page, _ := st.ListEnvelopes(ctx, ListQuery{
		ListQuery: envelope.ListQuery{Limit: 10},
		Accounts:  []string{"new"},
	}, r)
	if len(page.Envelopes) != 1 {
		t.Fatalf("expected 1 envelope under new name, got %d", len(page.Envelopes))
	}
	if page.Envelopes[0].Account != "new" {
		t.Errorf("envelope.account = %q, want new", page.Envelopes[0].Account)
	}
	want := "gmail:new:r1"
	if page.Envelopes[0].ID != want {
		t.Errorf("envelope.id = %q, want %q", page.Envelopes[0].ID, want)
	}
}

func TestSchemaMismatchRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open fresh: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE schema_meta SET v='999' WHERE k='version'`); err != nil {
		t.Fatalf("bump version: %v", err)
	}
	st.Close()

	_, err = Open(path)
	if err == nil {
		t.Fatal("expected schema mismatch error")
	}
	if !strings.Contains(err.Error(), "schema mismatch") {
		t.Errorf("got error %v, want schema mismatch", err)
	}
}
