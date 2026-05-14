package envelope

import (
	"context"
	"sort"
	"strings"

	"github.com/oflabs44/mbx/internal/mbxid"
)

// Thread is the JSON shape `mbx envelope thread` emits. Envelopes are in
// chronological order (oldest first). DepthMap is keyed by mbx-ID string;
// the root envelope of the thread has depth 0, each reply depth = parent + 1.
type Thread struct {
	ThreadID  string         `json:"thread_id"`
	Envelopes []Envelope     `json:"envelopes"`
	DepthMap  map[string]int `json:"depth_map"`
}

// ThreadQuery carries the envelope mbx ID the thread is being requested
// for. The backend uses the provider-specific fields (Gmail thread id,
// IMAP folder + UID) and the account scope is implicit in the ID.
type ThreadQuery struct {
	ID mbxid.ID
}

// ThreadSearcher is the narrow consumer interface ThreadOf requires.
// Backends satisfy it by writing a matching method (Go interface idiom:
// defined at the consumer, not the implementor). A backend that can't
// thread — older IMAP servers without THREAD capability and without an
// addressable corpus to run the client-side algorithm against — simply
// doesn't satisfy this and the command returns provider.unsupported.
type ThreadSearcher interface {
	ThreadEnvelopes(ctx context.Context, q ThreadQuery) (Thread, error)
}

// ThreadOf is the domain entry point for `mbx envelope thread`. Mirrors
// envelope.List / envelope.ApplyFlags so handlers dispatch through the
// same single-call shape.
func ThreadOf(ctx context.Context, t ThreadSearcher, q ThreadQuery) (Thread, error) {
	return t.ThreadEnvelopes(ctx, q)
}

// ThreadNode is the input row for BuildGraph: an envelope plus the three
// header values the threading algorithm needs. Headers are passed
// separately rather than added to Envelope so the JSON output contract
// (commands.md) isn't broadened with thread-only fields.
type ThreadNode struct {
	Envelope   Envelope
	MessageID  string
	InReplyTo  string
	References []string
}

// BuildGraph is the client-side threading algorithm: pimalaya/core's
// single-pass approach (adjacency map keyed by canonicalized Message-ID,
// virtual root for orphans) with two mbx patches the plan calls out:
//
//   - t-5-4: canonicalize Message-IDs (trim < >, whitespace) before lookup
//     so values normalize across header-encoding quirks.
//   - t-5-5: when In-Reply-To is missing or doesn't resolve in the window,
//     fall back to the last References entry that resolves — recovers
//     threading on mailing-list traffic that drops In-Reply-To.
//
// The returned Thread is filtered to the connected component containing
// anchorID, chronologically sorted (oldest first), with DepthMap stamping
// each envelope's depth from the thread root (0). ThreadID is the mbx ID
// of the root envelope; callers that have a server-supplied thread id
// (Gmail's native threadId) override it.
//
// If anchorID isn't in nodes, returns the zero Thread.
func BuildGraph(nodes []ThreadNode, anchorID string) Thread {
	if len(nodes) == 0 {
		return Thread{}
	}

	msgIDToIdx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if id := canonicalizeMessageID(n.MessageID); id != "" {
			msgIDToIdx[id] = i
		}
	}

	parents := make([]int, len(nodes))
	for i, n := range nodes {
		parents[i] = -1
		if p := canonicalizeMessageID(n.InReplyTo); p != "" {
			if pi, ok := msgIDToIdx[p]; ok && pi != i {
				parents[i] = pi
				continue
			}
		}
		for j := len(n.References) - 1; j >= 0; j-- {
			r := canonicalizeMessageID(n.References[j])
			if r == "" {
				continue
			}
			if ri, ok := msgIDToIdx[r]; ok && ri != i {
				parents[i] = ri
				break
			}
		}
	}

	breakParentCycles(parents)

	children := make([][]int, len(nodes))
	for i, p := range parents {
		if p >= 0 {
			children[p] = append(children[p], i)
		}
	}

	depths := make([]int, len(nodes))
	for i := range depths {
		depths[i] = -1
	}
	for i, p := range parents {
		if p == -1 {
			bfsDepths(i, 0, children, depths)
		}
	}

	anchorIdx := -1
	for i, n := range nodes {
		if n.Envelope.ID == anchorID {
			anchorIdx = i
			break
		}
	}
	if anchorIdx == -1 {
		return Thread{}
	}

	root := anchorIdx
	for parents[root] != -1 {
		root = parents[root]
	}

	component := []int{}
	queue := []int{root}
	seen := map[int]bool{root: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		component = append(component, cur)
		for _, c := range children[cur] {
			if !seen[c] {
				seen[c] = true
				queue = append(queue, c)
			}
		}
	}

	envs := make([]Envelope, 0, len(component))
	depth := make(map[string]int, len(component))
	for _, idx := range component {
		envs = append(envs, nodes[idx].Envelope)
		depth[nodes[idx].Envelope.ID] = depths[idx]
	}
	sort.SliceStable(envs, func(i, j int) bool { return envs[i].Date.Before(envs[j].Date) })

	return Thread{
		ThreadID:  nodes[root].Envelope.ID,
		Envelopes: envs,
		DepthMap:  depth,
	}
}

// breakParentCycles ensures parent chains terminate. Cycles can arise
// from forged or mis-encoded References / In-Reply-To headers (mailing
// list relays, archive imports) and would otherwise hang the root-walk
// in BuildGraph. We detect each cycle and break it by promoting one
// member to a root (parent = -1). O(n) per node; n ≤ thread_window.
func breakParentCycles(parents []int) {
	for i := range parents {
		if parents[i] == -1 {
			continue
		}
		visited := map[int]bool{i: true}
		cur := i
		for parents[cur] != -1 {
			next := parents[cur]
			if visited[next] {
				parents[cur] = -1
				break
			}
			visited[next] = true
			cur = next
		}
	}
}

func bfsDepths(start, startDepth int, children [][]int, depths []int) {
	depths[start] = startDepth
	queue := []int{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			if depths[c] == -1 {
				depths[c] = depths[cur] + 1
				queue = append(queue, c)
			}
		}
	}
}

// canonicalizeMessageID strips surrounding whitespace and a single pair
// of angle brackets. Empty input or input that's only the brackets
// returns "". The local-part / domain-part case sensitivity from
// RFC 5322 is preserved — mail servers vary, so byte-equal is safer
// than aggressive lowercasing.
func canonicalizeMessageID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	return strings.TrimSpace(s)
}
