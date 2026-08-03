package parser

import (
	"testing"
)

func TestParseSQL(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "Basic SQL Select",
			input: "SELECT id, name FROM users WHERE id > 123",
		},
		{
			name:  "PGQ Match",
			input: "SELECT id FROM GRAPH_TABLE(my_graph MATCH (a)-[e]->(b))",
		},
		{
			name:  "Vector Similarity",
			input: "SELECT id FROM users WHERE SIMILARITY(v, [1.0, 0.5]) > 0.8 ORDER BY id DESC LIMIT 10",
		},
		{
			name:  "Between and In Exprs",
			input: "SELECT id FROM users WHERE id BETWEEN 1 AND 5 OR status NOT IN (1, 2, 3)",
		},
		{
			name:  "PGQ Match with quantifiers",
			input: "SELECT id FROM GRAPH_TABLE(g MATCH (a)-[e]->{1,3}(b))",
		},
		{
			name:  "PGQ Match ->+",
			input: "SELECT id FROM GRAPH_TABLE(g MATCH (a)-[e]->+(b))",
		},
		{
			name:  "PGQ Match ->*",
			input: "SELECT id FROM GRAPH_TABLE(g MATCH (a)-[e]->*(b))",
		},
		{
			name:  "pgvector L2 distance",
			input: "SELECT id FROM items ORDER BY embedding <-> '[0.1, 0.2]' LIMIT 10",
		},
		{
			name:  "INSERT",
			input: "INSERT INTO users (id, name) VALUES ('42', 'alice')",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc QueryDoc
			err := Parse([]byte(tt.input), &doc)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if len(doc.SelectStmts) == 0 && len(doc.InsertStmts) == 0 && len(doc.UpdateStmts) == 0 && len(doc.DeleteStmts) == 0 {
				t.Fatalf("Expected at least 1 statement, got none")
			}
		})
	}
}

func BenchmarkParse_SQL(b *testing.B) {
	input := []byte("SELECT a, b, c FROM GRAPH_TABLE(g MATCH (x)-[y]->(z)) WHERE SIMILARITY(x.vec, [1.0, 0.5]) > 0.8 ORDER BY id LIMIT 100")
	var doc QueryDoc
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Parse(input, &doc)
	}
}

func TestParseMatchPathQuantifiers(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantMin uint16
		wantMax uint16
	}{
		{"default (no quantifier)", "(a)-[e]->(b)", 0, 0},
		{"one-or-more ->+", "(a)-[e]->+(b)", 1, QuantUnbounded},
		{"zero-or-more ->*", "(a)-[e]->*(b)", 0, QuantUnbounded},
		{"range {1,3}", "(a)-[e]->{1,3}(b)", 1, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte("SELECT id FROM GRAPH_TABLE(g MATCH " + tt.input + ")")
			var doc QueryDoc
			if err := Parse(src, &doc); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			gt := &doc.GraphTables[0]
			mp := &doc.MatchPaths[gt.MatchPath.ID]
			edgeRef := doc.Nodes[mp.PathNodesStart+1]
			if edgeRef.Kind != NodeKindEdge {
				t.Fatalf("expected Edge at index 1, got %v", edgeRef.Kind)
			}
			e := &doc.Edges[edgeRef.ID]
			if e.QuantMin != tt.wantMin {
				t.Errorf("QuantMin: want %d, got %d", tt.wantMin, e.QuantMin)
			}
			if e.QuantMax != tt.wantMax {
				t.Errorf("QuantMax: want %d, got %d", tt.wantMax, e.QuantMax)
			}
		})
	}
}

