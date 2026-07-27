package engine

import (
	"testing"

	"kgai/internal/store"
)

// Context is the recall path agents read before changing code — it must serve
// only current head decisions. Superseded decisions live in the log and are
// reachable via History, never pinned into context by default.
func TestContextReturnsOnlyHeadDecisions(t *testing.T) {
	s, err := store.Init(t.TempDir()+"/store", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	e := New(s)

	_, err = e.Ingest(IngestInput{Decisions: []DecisionInput{{
		Title:     "Search hides sold-out products",
		Rationale: "Sold-out items clutter the results.",
		Mutations: []MutationInput{
			{Op: "upsert_element", Kind: "feature", Name: "product-search"},
			{Op: "set_prop", Element: "feature:product-search", Key: "show_sold_out", Value: "false"},
		},
	}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Touching the same element auto-supersedes the prior head decision.
	_, err = e.Ingest(IngestInput{Decisions: []DecisionInput{{
		Title:     "Sold-out products stay visible in search",
		Rationale: "Hiding them dropped organic traffic.",
		Mutations: []MutationInput{
			{Op: "set_prop", Element: "feature:product-search", Key: "show_sold_out", Value: "true"},
		},
	}}}, false)
	if err != nil {
		t.Fatal(err)
	}

	res, err := e.Context(ContextQuery{About: "product-search", Max: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) == 0 {
		t.Fatal("expected the element in context")
	}
	why := res.Items[0].Why
	if len(why) != 1 {
		t.Fatalf("context must return only the head decision, got %d entries: %+v", len(why), why)
	}
	if why[0].Title != "Sold-out products stay visible in search" {
		t.Fatalf("wrong head decision in context: %q", why[0].Title)
	}
	if !why[0].IsHead {
		t.Fatal("head decision must be marked is_head")
	}

	// The dead end must still be reachable on demand.
	hist, err := e.History("feature:product-search")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist.Decisions) != 2 {
		t.Fatalf("history must keep the superseded decision, got %d", len(hist.Decisions))
	}
}

// The head decisions are fetched only for the elements that survive ranking and
// truncation, so this guards the two things that decoupling can break: every item
// must carry ITS OWN decisions (not a neighbour's), and the recency tiebreak — now
// fed by a plain max(lamport) aggregation rather than by the head query — must still
// order elements newest first.
func TestContextAttachesWhyToTheRightElementsAfterTruncation(t *testing.T) {
	s, err := store.Init(t.TempDir()+"/store", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	e := New(s)

	names := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for _, n := range names {
		if _, err := e.Ingest(IngestInput{Decisions: []DecisionInput{{
			Title:     "decided " + n,
			Rationale: "because of " + n,
			Mutations: []MutationInput{{Op: "upsert_element", Kind: "feature", Name: n}},
		}}}, false); err != nil {
			t.Fatal(err)
		}
	}
	// Supersede the oldest element so a stale decision exists to leak into context.
	if _, err := e.Ingest(IngestInput{Decisions: []DecisionInput{{
		Title:     "revised alpha",
		Rationale: "alpha again",
		Mutations: []MutationInput{{Op: "set_prop", Element: "feature:alpha", Key: "k", Value: "v"}},
	}}}, false); err != nil {
		t.Fatal(err)
	}

	// Unfiltered: every element qualifies, so ranking is the recency tiebreak alone.
	// "alpha" was just re-decided, so it now sorts newest, ahead of "echo".
	res, err := e.Context(ContextQuery{Max: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != len(names) || res.Shown != 2 || res.Omitted != len(names)-2 {
		t.Fatalf("truncation accounting wrong: total=%d shown=%d omitted=%d", res.Total, res.Shown, res.Omitted)
	}
	want := []struct{ name, why string }{
		{"alpha", "revised alpha"},
		{"echo", "decided echo"},
	}
	for i, w := range want {
		got := res.Items[i]
		if got.Name != w.name {
			t.Fatalf("item %d: recency order broken, want %q got %q", i, w.name, got.Name)
		}
		if len(got.Why) != 1 {
			t.Fatalf("item %d (%s): want exactly the head decision, got %d: %+v", i, got.Name, len(got.Why), got.Why)
		}
		if got.Why[0].Title != w.why {
			t.Fatalf("item %d (%s): decisions attached to the wrong element, got %q want %q",
				i, got.Name, got.Why[0].Title, w.why)
		}
	}
}
