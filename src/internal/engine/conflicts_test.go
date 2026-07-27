package engine

import (
	"os"
	"path/filepath"
	"testing"

	"kgai/internal/store"
)

// mergeShards copies every writer's append-only shard into one store, which is what
// a completed sync leaves behind: two people's decisions side by side in one log.
func mergeShards(t *testing.T, dst *store.Store, srcs ...*store.Store) {
	t.Helper()
	for _, src := range srcs {
		dir := filepath.Join(src.Root, "log")
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, en := range entries {
			b, err := os.ReadFile(filepath.Join(dir, en.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst.Root, "log", en.Name()), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func ingest(t *testing.T, e *Engine, title string, muts ...MutationInput) {
	t.Helper()
	if _, err := e.Ingest(IngestInput{Decisions: []DecisionInput{{
		Title: title, Rationale: "because " + title, Mutations: muts,
	}}}, false); err != nil {
		t.Fatal(err)
	}
}

// Two writers deciding the same element concurrently is the case kgai promises to
// surface rather than silently pick a winner for. The reporting order must be fixed
// too: the same store has to describe a branch the same way on every machine and
// every run, or "deterministic" stops meaning anything to whoever reads the output.
func TestConflictsAreReportedInDeterministicOrder(t *testing.T) {
	root := t.TempDir()
	newStore := func(name, actor string) (*store.Store, *Engine) {
		s, err := store.Init(filepath.Join(root, name), actor, "")
		if err != nil {
			t.Fatal(err)
		}
		return s, New(s)
	}

	// Alice decides twice, so her surviving head outranks Bob's single decision.
	alice, ea := newStore("alice", "alice")
	ingest(t, ea, "Invoices hidden when draft",
		MutationInput{Op: "upsert_element", Kind: "feature", Name: "Invoice"})
	ingest(t, ea, "Draft invoices stay visible",
		MutationInput{Op: "set_prop", Element: "feature:Invoice", Key: "visibility", Value: "always"})

	bob, eb := newStore("bob", "bob")
	ingest(t, eb, "Invoice rendering moved out of pricing",
		MutationInput{Op: "upsert_element", Kind: "feature", Name: "Invoice"})

	merged, em := newStore("merged", "merged")
	mergeShards(t, merged, alice, bob)
	if _, err := em.Rebuild(); err != nil {
		t.Fatal(err)
	}

	groups, err := em.Conflicts("")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("want one conflicted element, got %d: %+v", len(groups), groups)
	}
	if n := len(groups[0].Heads); n != 2 {
		t.Fatalf("want both sides of the branch, got %d heads: %+v", n, groups[0])
	}
	// Newest head first. Replay inserts in ascending lamport order, so a result that
	// merely reflects scan order would put Bob's older decision first.
	if got, want := groups[0].Titles[0], "Draft invoices stay visible"; got != want {
		t.Fatalf("heads are not ordered newest-first: got %q, want %q (all: %v)", got, want, groups[0].Titles)
	}
	if len(groups[0].Titles) != len(groups[0].Heads) {
		t.Fatalf("heads and titles must line up: %+v", groups[0])
	}

	// Same store, repeated reads: identical every time.
	for i := 0; i < 5; i++ {
		again, err := em.Conflicts("")
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != len(groups) {
			t.Fatalf("read %d returned %d groups, first read had %d", i, len(again), len(groups))
		}
		for j := range again {
			if again[j].ElementID != groups[j].ElementID {
				t.Fatalf("read %d: element order changed at %d: %q vs %q", i, j, again[j].ElementID, groups[j].ElementID)
			}
			for k := range again[j].Heads {
				if again[j].Heads[k] != groups[j].Heads[k] {
					t.Fatalf("read %d: head order changed for %s: %v vs %v",
						i, again[j].Name, again[j].Heads, groups[j].Heads)
				}
			}
		}
	}
}
