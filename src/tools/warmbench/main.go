// warmbench isolates read-query cost at scale: opens the projection ONCE (as a
// persistent server would) and times the individual Cypher queries behind
// `context`/`search`, warm. Separates per-process graph-open cost from query cost.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"kgai/internal/graph"
	"kgai/internal/store"
)

func ms(d time.Duration) string { return fmt.Sprintf("%8.1fms", float64(d.Microseconds())/1000) }

func main() {
	iters := flag.Int("iters", 5, "warm repetitions per query")
	flag.Parse()

	s, err := store.Open(os.Getenv("KGAI_STORE"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "open store:", err)
		os.Exit(1)
	}
	g, err := graph.Open(s.GraphPath(), true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open graph:", err)
		os.Exit(1)
	}
	defer g.Close()

	// grab a sample of element ids to scope the "top N" query
	sample, _ := g.Raw(`MATCH (e:Element) RETURN e.id AS id LIMIT 15`)
	var ids []string
	for _, r := range sample {
		ids = append(ids, "'"+fmt.Sprint(r["id"])+"'")
	}
	idList := strings.Join(ids, ",")

	bench := []struct{ name, q string }{
		{"CURRENT: global head anti-join", `MATCH (d:Decision)-[s:SHAPES]->(e:Element)
			WHERE s.authority = true
			  AND NOT EXISTS { MATCH (d2:Decision)-[:SUPERSEDES]->(d), (d2)-[s2:SHAPES]->(e) WHERE s2.authority = true }
			RETURN e.id AS eid, d.id AS did ORDER BY d.lamport DESC`},
		{"ALT a) max-lamport aggregation (scoring)", `MATCH (d:Decision)-[s:SHAPES]->(e:Element)
			WHERE s.authority = true RETURN e.id AS eid, max(d.lamport) AS ml`},
		{"ALT b) head anti-join SCOPED to 15 elems", `MATCH (d:Decision)-[s:SHAPES]->(e:Element)
			WHERE s.authority = true AND e.id IN [` + idList + `]
			  AND NOT EXISTS { MATCH (d2:Decision)-[:SUPERSEDES]->(d), (d2)-[s2:SHAPES]->(e) WHERE s2.authority = true }
			RETURN e.id AS eid, d.id AS did ORDER BY d.lamport DESC`},
	}
	for _, b := range bench {
		var best time.Duration
		var rows int
		for i := 0; i < *iters; i++ {
			t := time.Now()
			r, _ := g.Raw(b.q)
			d := time.Since(t)
			rows = len(r)
			if i == 0 || d < best {
				best = d
			}
		}
		fmt.Printf("%-42s %s   (%d rows)\n", b.name, ms(best), rows)
	}
}
