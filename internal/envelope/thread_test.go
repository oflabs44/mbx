package envelope

import (
	"reflect"
	"testing"
	"time"
)

func node(id, msgID, inReplyTo string, refs []string, date time.Time) ThreadNode {
	return ThreadNode{
		Envelope:   Envelope{ID: id, Date: date},
		MessageID:  msgID,
		InReplyTo:  inReplyTo,
		References: refs,
	}
}

func TestCanonicalizeMessageID(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"  ":              "",
		"<>":              "",
		"<a@b>":           "a@b",
		" <a@b> ":         "a@b",
		"a@b":             "a@b",
		"<a@b":            "a@b",
		"a@b>":            "a@b",
		" < a@b > ":       "a@b",
		"<A@B.com>":       "A@B.com",
		"<a@b><c@d>":      "a@b><c@d",
		"<not-trimmed @>": "not-trimmed @",
	}
	for in, want := range cases {
		if got := canonicalizeMessageID(in); got != want {
			t.Errorf("canonicalizeMessageID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildGraph_LinearChain(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []ThreadNode{
		node("id-c", "c@x", "<b@x>", []string{"<a@x>", "<b@x>"}, t0.Add(2*time.Hour)),
		node("id-a", "a@x", "", nil, t0),
		node("id-b", "b@x", "<a@x>", []string{"<a@x>"}, t0.Add(time.Hour)),
	}
	th := BuildGraph(nodes, "id-b")

	if th.ThreadID != "id-a" {
		t.Fatalf("ThreadID = %q, want id-a (root)", th.ThreadID)
	}
	wantIDs := []string{"id-a", "id-b", "id-c"}
	gotIDs := make([]string, len(th.Envelopes))
	for i, e := range th.Envelopes {
		gotIDs[i] = e.ID
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("envelope order = %v, want %v (chronological)", gotIDs, wantIDs)
	}
	wantDepth := map[string]int{"id-a": 0, "id-b": 1, "id-c": 2}
	if !reflect.DeepEqual(th.DepthMap, wantDepth) {
		t.Errorf("DepthMap = %v, want %v", th.DepthMap, wantDepth)
	}
}

func TestBuildGraph_ReferencesFallback(t *testing.T) {
	// id-c has no In-Reply-To but References lists [a, b]. The last
	// resolvable entry (b) should become its parent.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []ThreadNode{
		node("id-a", "a@x", "", nil, t0),
		node("id-b", "b@x", "<a@x>", []string{"<a@x>"}, t0.Add(time.Hour)),
		node("id-c", "c@x", "", []string{"<a@x>", "<b@x>"}, t0.Add(2*time.Hour)),
	}
	th := BuildGraph(nodes, "id-c")

	if th.DepthMap["id-c"] != 2 {
		t.Errorf("id-c depth = %d, want 2 (parent via References fallback)", th.DepthMap["id-c"])
	}
}

func TestBuildGraph_OrphanFallsToRoot(t *testing.T) {
	// id-b's In-Reply-To points outside the window; should be treated as
	// a thread root (depth 0), and the anchor's component contains only
	// id-b.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []ThreadNode{
		node("id-a", "a@x", "", nil, t0),
		node("id-b", "b@x", "<missing@x>", []string{"<missing@x>"}, t0.Add(time.Hour)),
	}
	th := BuildGraph(nodes, "id-b")

	if th.ThreadID != "id-b" {
		t.Errorf("ThreadID = %q, want id-b (orphan becomes its own root)", th.ThreadID)
	}
	if len(th.Envelopes) != 1 || th.Envelopes[0].ID != "id-b" {
		t.Errorf("Envelopes = %+v, want [id-b]", th.Envelopes)
	}
	if th.DepthMap["id-b"] != 0 {
		t.Errorf("id-b depth = %d, want 0", th.DepthMap["id-b"])
	}
}

func TestBuildGraph_AnchorNotInInput(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []ThreadNode{node("id-a", "a@x", "", nil, t0)}
	th := BuildGraph(nodes, "id-missing")
	if th.ThreadID != "" || len(th.Envelopes) != 0 {
		t.Errorf("anchor not in input should yield zero Thread, got %+v", th)
	}
}

func TestBuildGraph_FiltersOtherThreads(t *testing.T) {
	// Two disjoint threads in the window; anchor is in the second.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []ThreadNode{
		node("id-a1", "a1@x", "", nil, t0),
		node("id-a2", "a2@x", "<a1@x>", nil, t0.Add(time.Hour)),
		node("id-b1", "b1@x", "", nil, t0.Add(2*time.Hour)),
		node("id-b2", "b2@x", "<b1@x>", nil, t0.Add(3*time.Hour)),
	}
	th := BuildGraph(nodes, "id-b2")
	if th.ThreadID != "id-b1" {
		t.Errorf("ThreadID = %q, want id-b1", th.ThreadID)
	}
	if len(th.Envelopes) != 2 {
		t.Errorf("Envelopes count = %d, want 2 (only b-thread)", len(th.Envelopes))
	}
	for _, e := range th.Envelopes {
		if e.ID != "id-b1" && e.ID != "id-b2" {
			t.Errorf("unexpected envelope %q in thread", e.ID)
		}
	}
}

func TestBuildGraph_MutualInReplyToDoesNotHang(t *testing.T) {
	// Two nodes each name the other as parent. Without cycle-breaking,
	// the root-walk loops forever. With it, one of the two becomes the
	// component root and depths assign cleanly.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []ThreadNode{
		node("id-a", "a@x", "<b@x>", nil, t0),
		node("id-b", "b@x", "<a@x>", nil, t0.Add(time.Hour)),
	}
	done := make(chan struct{})
	var th Thread
	go func() {
		th = BuildGraph(nodes, "id-a")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("BuildGraph hung on a parent cycle")
	}
	if len(th.Envelopes) != 2 {
		t.Errorf("Envelopes = %d, want 2 (both members of the cycle)", len(th.Envelopes))
	}
	for _, e := range th.Envelopes {
		if d, ok := th.DepthMap[e.ID]; !ok || d < 0 {
			t.Errorf("envelope %s depth = %d (ok=%v), want >= 0", e.ID, d, ok)
		}
	}
}

func TestBuildGraph_SelfInReplyToTreatedAsRoot(t *testing.T) {
	// A pathological node whose In-Reply-To is its own Message-ID; the
	// pi != i guard prevents self-parenting and the node becomes a root.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []ThreadNode{
		node("id-a", "a@x", "<a@x>", []string{"<a@x>"}, t0),
	}
	th := BuildGraph(nodes, "id-a")
	if th.ThreadID != "id-a" || th.DepthMap["id-a"] != 0 {
		t.Errorf("self-reference should be a root; got ThreadID=%q depth=%d", th.ThreadID, th.DepthMap["id-a"])
	}
}

func TestBuildGraph_AllEmptyMessageIDsEachIsItsOwnRoot(t *testing.T) {
	// No Message-IDs means no node can be a parent. Every node orphans
	// to root; anchor's component is just itself.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []ThreadNode{
		node("id-a", "", "<some-ref@x>", []string{"<x@x>"}, t0),
		node("id-b", "", "", nil, t0.Add(time.Hour)),
	}
	th := BuildGraph(nodes, "id-b")
	if th.ThreadID != "id-b" || len(th.Envelopes) != 1 {
		t.Errorf("empty-msgid corpus: anchor should be alone at root; got ThreadID=%q envelopes=%d", th.ThreadID, len(th.Envelopes))
	}
}

func TestBuildGraph_CanonicalizesAcrossAngleBrackets(t *testing.T) {
	// Parent's Message-ID has no brackets; child's In-Reply-To wraps in
	// brackets. After canonicalization they must match.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nodes := []ThreadNode{
		node("id-a", "a@x", "", nil, t0),
		node("id-b", "b@x", "<a@x>", nil, t0.Add(time.Hour)),
	}
	th := BuildGraph(nodes, "id-b")
	if th.DepthMap["id-b"] != 1 {
		t.Errorf("id-b depth = %d, want 1 (parent reachable across bracket forms)", th.DepthMap["id-b"])
	}
}
