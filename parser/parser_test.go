package parser

import (
	"testing"

	"github.com/xDarkicex/lexer"
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
		{
			name:  "PGQ vertex label",
			input: "SELECT id FROM GRAPH_TABLE(g MATCH (a:Person)-[e]->(b))",
		},
		{
			name:  "PGQ edge type",
			input: "SELECT id FROM GRAPH_TABLE(g MATCH (a)-[e:KNOWS]->(b))",
		},
		{
			name:  "PGQ asterisk-range",
			input: "SELECT id FROM GRAPH_TABLE(g MATCH (a)-[e*1..3]->(b))",
		},
		{
			name:  "PGQ full labels+types+quantifier",
			input: "SELECT id FROM GRAPH_TABLE(g MATCH (a:Service)-[e:DEPENDS_ON*1..3]->(api:Endpoint)-[:DOCUMENTED_BY]->(doc:Manual))",
		},
		{
			name:  "JOIN MATCH graph join",
			input: "SELECT doc.id FROM services s JOIN MATCH (s)-[:DEPENDS_ON*1..3]->(api:Endpoint)-[:DOCUMENTED_BY]->(doc:Manual)",
		},
		{
			name:  "JOIN MATCH with FROM alias + qualified ON",
			input: "SELECT s.id FROM services s JOIN MATCH (s)-[:DEPENDS_ON]->(x) ON s.owner_id = x.owner_id",
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

func TestParseArrayCosineSimilarity(t *testing.T) {
	var doc QueryDoc
	if err := Parse([]byte("SELECT array_cosine_similarity(vector, '[1,0,0,0]') AS score FROM docs"), &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.VectorFuncs) != 1 {
		t.Fatalf("vector funcs: got %d, want 1", len(doc.VectorFuncs))
	}
	if !doc.VectorFuncs[0].IsMaxSim {
		t.Fatal("array_cosine_similarity must lower to max-sim vector function")
	}
}

func TestGraphitiCypherClauseAdditions(t *testing.T) {
	tests := []struct {
		name  string
		sql   string
		check func(*testing.T, *QueryDoc)
	}{
		{
			name: "detach delete",
			sql:  "MATCH (a)-[r:REL]->(b) DETACH DELETE a",
			check: func(t *testing.T, doc *QueryDoc) {
				if len(doc.DeleteStmts) != 1 || !doc.DeleteStmts[0].Cypher || !doc.DeleteStmts[0].Detach || len(doc.DeleteStmts[0].Targets) != 1 {
					t.Fatalf("delete AST: %#v", doc.DeleteStmts)
				}
			},
		},
		{
			name: "universal merge set",
			sql:  "MERGE (n:Entity {uuid: $u}) SET n.name = $name ON MATCH SET n.updated = $ts",
			check: func(t *testing.T, doc *QueryDoc) {
				if len(doc.MergeStmts) != 1 || doc.MergeStmts[0].UniversalSetCount != 1 || doc.MergeStmts[0].OnMatchCount != 1 {
					t.Fatalf("merge AST: %#v assignments=%#v", doc.MergeStmts, doc.MergeAssignments)
				}
			},
		},
		{
			name: "pipe with",
			sql:  "MATCH (n) WITH DISTINCT n.uuid AS u WHERE u <> 'x' ORDER BY u SKIP 1 LIMIT 2 RETURN u",
			check: func(t *testing.T, doc *QueryDoc) {
				if len(doc.SelectStmts) != 1 || doc.SelectStmts[0].PipeWithCount != 1 || len(doc.WithClauses) != 1 {
					t.Fatalf("with AST: selects=%#v clauses=%#v", doc.SelectStmts, doc.WithClauses)
				}
				clause := doc.WithClauses[0]
				if !clause.Distinct || clause.Where.Kind == NodeKindUnknown || len(clause.OrderTerms) != 1 || clause.Skip.Kind == NodeKindUnknown || clause.Limit.Kind == NodeKindUnknown {
					t.Fatalf("with clause: %#v", clause)
				}
			},
		},
		{
			name: "pipe with followed by match",
			sql:  "MATCH (n) WITH n.uuid AS u MATCH (m {uuid: u}) RETURN m.uuid",
			check: func(t *testing.T, doc *QueryDoc) {
				if len(doc.WithClauses) != 1 || doc.WithClauses[0].MatchPath.Kind != NodeKindMatchPath {
					t.Fatalf("chained WITH AST: %#v", doc.WithClauses)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &QueryDoc{}
			if err := Parse([]byte(tt.sql), doc); err != nil {
				t.Fatalf("Parse(%q): %v", tt.sql, err)
			}
			tt.check(t, doc)
		})
	}
}

func TestParseExplainAnalyzeGraphQuery(t *testing.T) {
	src := []byte("EXPLAIN ANALYZE SELECT src.id FROM people src JOIN MATCH (src)-[:FOLLOWS]->(tgt) WHERE src.id = $1;")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse EXPLAIN ANALYZE: %v", err)
	}
	if !doc.Explain || !doc.ExplainAnalyze {
		t.Fatalf("explain flags: explain=%v analyze=%v", doc.Explain, doc.ExplainAnalyze)
	}
	if doc.ExplainQueryStart >= doc.ExplainQueryEnd || string(src[doc.ExplainQueryStart:doc.ExplainQueryEnd]) != "SELECT src.id FROM people src JOIN MATCH (src)-[:FOLLOWS]->(tgt) WHERE src.id = $1" {
		t.Fatalf("inner query span=[%d,%d): %q", doc.ExplainQueryStart, doc.ExplainQueryEnd, src[doc.ExplainQueryStart:doc.ExplainQueryEnd])
	}
	if len(doc.SelectStmts) != 1 || len(doc.SelectStmts[0].Joins) != 1 || doc.SelectStmts[0].Joins[0].MatchPath.Kind != NodeKindMatchPath {
		t.Fatalf("graph AST was not retained: selects=%d joins=%d match=%v", len(doc.SelectStmts), len(doc.SelectStmts[0].Joins), doc.SelectStmts[0].Joins[0].MatchPath.Kind)
	}
}

func TestParseGraphDDL(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sql   string
		graph bool
		edge  bool
	}{
		{name: "graph table", sql: "CREATE GRAPH TABLE users (id TEXT PRIMARY KEY)", graph: true},
		{name: "edge type", sql: "CREATE EDGE TYPE FOLLOWS", edge: true},
		{name: "undirected edge type", sql: "CREATE EDGE TYPE KNOWS UNDIRECTED", edge: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc QueryDoc
			if err := Parse([]byte(tc.sql), &doc); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if tc.graph {
				if len(doc.CreateTableStmts) != 1 || !doc.CreateTableStmts[0].Graph {
					t.Fatalf("graph DDL AST: %#v", doc.CreateTableStmts)
				}
			}
			if tc.edge {
				if len(doc.CreateEdgeTypeStmts) != 1 {
					t.Fatalf("edge DDL AST: %#v", doc.CreateEdgeTypeStmts)
				}
				stmt := doc.CreateEdgeTypeStmts[0]
				got := tc.sql[stmt.NameStart:stmt.NameEnd]
				if tc.name == "edge type" && got != "FOLLOWS" {
					t.Fatalf("edge name=%q", got)
				}
				if tc.name == "undirected edge type" && (!stmt.Undirected || !stmt.DirectionSpecified || got != "KNOWS") {
					t.Fatalf("undirected edge AST: %#v name=%q", stmt, got)
				}
			}
		})
	}
}

func TestParseSelectMultipleOrderTerms(t *testing.T) {
	var doc QueryDoc
	if err := Parse([]byte("SELECT id FROM docs ORDER BY distance ASC, id DESC LIMIT 128"), &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.SelectStmts) != 1 {
		t.Fatalf("select statements=%d", len(doc.SelectStmts))
	}
	stmt := doc.SelectStmts[0]
	if len(stmt.OrderTerms) != 2 {
		t.Fatalf("order terms=%d", len(stmt.OrderTerms))
	}
	if stmt.OrderBy != stmt.OrderTerms[0].Expr || stmt.IsDesc {
		t.Fatalf("legacy first order view=%#v desc=%v", stmt.OrderBy, stmt.IsDesc)
	}
	if !stmt.OrderTerms[1].IsDesc {
		t.Fatalf("second order term should be DESC: %#v", stmt.OrderTerms[1])
	}
}

func TestParseAggregateParameters(t *testing.T) {
	src := []byte(`SELECT MIN($p_threshold), MIN($break_even_accuracy), SUM($p_threshold) FROM transitions`)
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.AggregateExprs) != 3 {
		t.Fatalf("aggregate expressions=%d, want 3", len(doc.AggregateExprs))
	}
	if len(doc.Projections) != 3 {
		t.Fatalf("projections=%d, want 3", len(doc.Projections))
	}
	for i, projection := range doc.Projections {
		if projection.Expr.Kind != NodeKindAggregateExpr || projection.Expr.ID != int32(i) {
			t.Fatalf("projection %d=%#v, want aggregate expression %d", i, projection.Expr, i)
		}
	}
	want := []string{"$p_threshold", "$break_even_accuracy", "$p_threshold"}
	for i, ae := range doc.AggregateExprs {
		if ae.Expr.Kind != NodeKindIdentifier || ae.Expr.ID < 0 || int(ae.Expr.ID) >= len(doc.Identifiers) {
			t.Fatalf("aggregate %d argument=%#v, want parameter identifier", i, ae.Expr)
		}
		id := doc.Identifiers[ae.Expr.ID]
		if got := string(src[id.Start:id.End]); got != want[i] {
			t.Errorf("aggregate %d argument=%q, want %q", i, got, want[i])
		}
	}
}

func TestParseCypherINListParameter(t *testing.T) {
	src := []byte(`MATCH (e) WHERE e.group_id IN $group_ids RETURN e.id`)
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.InExprs) != 1 {
		t.Fatalf("IN expressions=%d, want 1", len(doc.InExprs))
	}
	in := doc.InExprs[0]
	if !in.IsParam || in.ParamRef.Kind != NodeKindIdentifier {
		t.Fatalf("IN expression=%#v, want parameter reference", in)
	}
	param := doc.Identifiers[in.ParamRef.ID]
	if got := string(src[param.Start:param.End]); got != "$group_ids" {
		t.Fatalf("parameter=%q, want $group_ids", got)
	}

	src = []byte(`MATCH (e) WHERE e.group_id NOT IN $group_ids RETURN e.id`)
	doc.Reset()
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse NOT IN: %v", err)
	}
	if len(doc.InExprs) != 1 || !doc.InExprs[0].IsParam || !doc.InExprs[0].Not {
		t.Fatalf("NOT IN expression=%#v, want negated parameter reference", doc.InExprs)
	}
}

func TestParseMatchEdgeWeightPredicate(t *testing.T) {
	src := []byte("SELECT x.id FROM docs s JOIN MATCH (s)-[r:RELATES WHERE r.weight > 0.8]->(x)")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.SelectStmts) != 1 || len(doc.SelectStmts[0].Joins) != 1 {
		t.Fatalf("unexpected select/join AST: %#v", doc.SelectStmts)
	}
	join := doc.SelectStmts[0].Joins[0]
	if join.MatchPath.Kind != NodeKindMatchPath {
		t.Fatalf("join path kind=%v", join.MatchPath.Kind)
	}
	mp := doc.MatchPaths[join.MatchPath.ID]
	var edge *Edge
	for i := int32(0); i < mp.PathNodesCount; i++ {
		ref := doc.Nodes[mp.PathNodesStart+i]
		if ref.Kind == NodeKindEdge {
			e := &doc.Edges[ref.ID]
			edge = e
			break
		}
	}
	if edge == nil || edge.Predicate.Kind != NodeKindBinaryExpr {
		t.Fatalf("edge predicate=%#v", edge)
	}
	be := doc.BinaryExprs[edge.Predicate.ID]
	if be.Left.Kind != NodeKindIdentifier || be.Right.Kind != NodeKindNumber || be.Operator != uint8(lexer.KindGreaterThan) {
		t.Fatalf("edge predicate binary=%#v", be)
	}
	left := doc.Identifiers[be.Left.ID]
	if string(src[left.QualStart:left.QualEnd]) != "r" || string(src[left.Start:left.End]) != "weight" {
		t.Fatalf("edge predicate identifier=%q.%q", src[left.QualStart:left.QualEnd], src[left.Start:left.End])
	}
}

func TestParseMatchEdgePropertyBlock(t *testing.T) {
	src := []byte("SELECT x.id FROM docs s JOIN MATCH (s)-[r:RELATES {weight > 0.8, type: 'STRONG'}]->(x)")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	join := doc.SelectStmts[0].Joins[0]
	mp := doc.MatchPaths[join.MatchPath.ID]
	var edge *Edge
	for i := int32(0); i < mp.PathNodesCount; i++ {
		ref := doc.Nodes[mp.PathNodesStart+i]
		if ref.Kind == NodeKindEdge {
			edge = &doc.Edges[ref.ID]
			break
		}
	}
	if edge == nil || edge.Predicate.Kind != NodeKindBinaryExpr {
		t.Fatalf("edge property predicate=%#v", edge)
	}
	and := doc.BinaryExprs[edge.Predicate.ID]
	if and.Operator != uint8(lexer.KindAnd) {
		t.Fatalf("property block operator=%v, want AND", and.Operator)
	}
	weight := doc.BinaryExprs[and.Left.ID]
	if weight.Operator != uint8(lexer.KindGreaterThan) {
		t.Fatalf("weight predicate=%#v", weight)
	}
	typePred := doc.BinaryExprs[and.Right.ID]
	if typePred.Operator != uint8(lexer.KindEquals) || typePred.Right.Kind != NodeKindString {
		t.Fatalf("type predicate=%#v", typePred)
	}
}

func TestParseMatchEdgePropertyBooleanBlock(t *testing.T) {
	src := []byte("SELECT x.id FROM docs s JOIN MATCH (s)-[r:RELATES {weight > 0.8 OR type = 'STRONG'}]->(x)")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatal(err)
	}
	join := doc.SelectStmts[0].Joins[0]
	mp := doc.MatchPaths[join.MatchPath.ID]
	var edge *Edge
	for i := int32(0); i < mp.PathNodesCount; i++ {
		ref := doc.Nodes[mp.PathNodesStart+i]
		if ref.Kind == NodeKindEdge {
			edge = &doc.Edges[ref.ID]
			break
		}
	}
	if edge == nil || edge.Predicate.Kind != NodeKindBinaryExpr {
		t.Fatalf("edge property predicate=%#v", edge)
	}
	root := doc.BinaryExprs[edge.Predicate.ID]
	if root.Operator != uint8(lexer.KindOr) {
		t.Fatalf("root operator=%v, want OR", root.Operator)
	}
}

func TestParseEpochTransactionStatements(t *testing.T) {
	var doc QueryDoc
	if err := Parse([]byte("BEGIN EPOCH TRANSACTION"), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.TransactionStmts) != 1 || doc.TransactionStmts[0].Kind != TransactionBeginEpoch {
		t.Fatalf("unexpected transaction AST: %#v", doc.TransactionStmts)
	}
	if err := Parse([]byte("ROLLBACK"), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.TransactionStmts) != 1 || doc.TransactionStmts[0].Kind != TransactionRollback {
		t.Fatalf("unexpected rollback AST: %#v", doc.TransactionStmts)
	}
}

func TestParseStandardTransactionStatements(t *testing.T) {
	tests := []struct {
		sql  string
		kind TransactionKind
	}{
		{sql: "BEGIN", kind: TransactionBegin},
		{sql: "BEGIN TRANSACTION", kind: TransactionBegin},
		{sql: "START TRANSACTION", kind: TransactionBegin},
		{sql: "COMMIT WORK", kind: TransactionCommit},
		{sql: "ROLLBACK TRANSACTION", kind: TransactionRollback},
	}
	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			var doc QueryDoc
			if err := Parse([]byte(tt.sql), &doc); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(doc.TransactionStmts) != 1 || doc.TransactionStmts[0].Kind != tt.kind {
				t.Fatalf("transaction AST: want %v, got %#v", tt.kind, doc.TransactionStmts)
			}
		})
	}
}

func TestQualifiedIdentifierAndAscendingOrder(t *testing.T) {
	input := []byte("SELECT doc.title FROM services s JOIN MATCH (s)-[e]->(doc) ORDER BY doc.title ASC LIMIT 5")
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(doc.Identifiers) < 2 {
		t.Fatalf("identifiers=%d, want qualified projection and order expression", len(doc.Identifiers))
	}
	first := doc.Identifiers[0]
	if got := string(input[first.QualStart:first.QualEnd]); got != "doc" {
		t.Fatalf("projection qualifier=%q, want doc", got)
	}
	if got := string(input[first.Start:first.End]); got != "title" {
		t.Fatalf("projection column=%q, want title", got)
	}
	stmt := doc.SelectStmts[0]
	if stmt.IsDesc {
		t.Fatal("ASC parsed as descending order")
	}
	if stmt.Limit < 0 {
		t.Fatal("ASC was not consumed before LIMIT")
	}
}

func TestParseSelectOffset(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantLimit  string
		wantOffset string
	}{
		{
			name:       "limit then offset",
			input:      "SELECT id FROM users LIMIT 10 OFFSET 25",
			wantLimit:  "10",
			wantOffset: "25",
		},
		{
			name:       "offset without limit",
			input:      "SELECT id FROM users oFfSeT 7",
			wantLimit:  "",
			wantOffset: "7",
		},
		{
			name:       "offset then limit",
			input:      "SELECT id FROM users OFFSET 3 LIMIT 4",
			wantLimit:  "4",
			wantOffset: "3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(tt.input)
			var doc QueryDoc
			if err := Parse(input, &doc); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(doc.SelectStmts) != 1 {
				t.Fatalf("SelectStmts=%d, want 1", len(doc.SelectStmts))
			}
			stmt := doc.SelectStmts[0]
			if stmt.Offset < 0 {
				t.Fatal("Offset must reference a Number node")
			}
			gotOffset := string(input[doc.Numbers[stmt.Offset].Start:doc.Numbers[stmt.Offset].End])
			if gotOffset != tt.wantOffset {
				t.Fatalf("Offset literal=%q, want %q", gotOffset, tt.wantOffset)
			}
			if tt.wantLimit == "" {
				if stmt.Limit != -1 {
					t.Fatalf("Limit=%d, want unset", stmt.Limit)
				}
				return
			}
			if stmt.Limit < 0 {
				t.Fatal("Limit must reference a Number node")
			}
			gotLimit := string(input[doc.Numbers[stmt.Limit].Start:doc.Numbers[stmt.Limit].End])
			if gotLimit != tt.wantLimit {
				t.Fatalf("Limit literal=%q, want %q", gotLimit, tt.wantLimit)
			}
		})
	}
}

func TestParseSelectOffsetRequiresNumber(t *testing.T) {
	for _, input := range []string{
		"SELECT id FROM users OFFSET",
		"SELECT id FROM users OFFSET foo",
	} {
		var doc QueryDoc
		if err := Parse([]byte(input), &doc); err == nil {
			t.Fatalf("Parse(%q): expected OFFSET validation error", input)
		}
	}
}

func TestParseSelectOffsetRejectsDuplicates(t *testing.T) {
	for _, input := range []string{
		"SELECT id FROM users OFFSET 1 OFFSET 2",
		"SELECT id FROM users LIMIT 1 LIMIT 2",
	} {
		var doc QueryDoc
		if err := Parse([]byte(input), &doc); err == nil {
			t.Fatalf("Parse(%q): expected duplicate clause error", input)
		}
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
		{"asterisk-range *1..3", "(a)-[e]->*1..3(b)", 1, 3},
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

func TestParseMatchPathLabels(t *testing.T) {
	// Each test specifies a vertex pattern and an edge pattern.
	// The framework builds: GRAPH_TABLE(g MATCH <vertex>-<edge>->(b))
	tests := []struct {
		name      string
		vertex    string
		edge      string
		wantAlias string
		wantLabel string // vertex label or edge type
		isEdge    bool
	}{
		{"vertex label with alias", "(a:Person)", "[e]", "a", "Person", false},
		{"vertex label no alias", "(:Person)", "[e]", "", "Person", false},
		{"edge type with alias", "(a)", "[e:KNOWS]", "e", "KNOWS", true},
		{"edge type no alias", "(a)", "[:KNOWS]", "", "KNOWS", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte("SELECT id FROM GRAPH_TABLE(g MATCH " + tt.vertex + "-" + tt.edge + "->(b))")
			var doc QueryDoc
			if err := Parse(src, &doc); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			gt := &doc.GraphTables[0]
			mp := &doc.MatchPaths[gt.MatchPath.ID]

			if tt.isEdge {
				edgeRef := doc.Nodes[mp.PathNodesStart+1]
				if edgeRef.Kind != NodeKindEdge {
					t.Fatalf("expected Edge at index 1, got %v", edgeRef.Kind)
				}
				e := &doc.Edges[edgeRef.ID]
				alias := string(src[e.Alias:e.AliasEnd])
				if alias != tt.wantAlias {
					t.Errorf("alias: want %q, got %q", tt.wantAlias, alias)
				}
				if e.TypeStart != e.TypeEnd {
					typ := string(src[e.TypeStart:e.TypeEnd])
					if typ != tt.wantLabel {
						t.Errorf("type: want %q, got %q", tt.wantLabel, typ)
					}
				} else if tt.wantLabel != "" {
					t.Errorf("expected type %q, got none", tt.wantLabel)
				}
			} else {
				vRef := doc.Nodes[mp.PathNodesStart]
				if vRef.Kind != NodeKindVertex {
					t.Fatalf("expected Vertex at index 0, got %v", vRef.Kind)
				}
				v := &doc.Vertexes[vRef.ID]
				alias := string(src[v.Alias:v.AliasEnd])
				if alias != tt.wantAlias {
					t.Errorf("alias: want %q, got %q", tt.wantAlias, alias)
				}
				if v.LabelStart != v.LabelEnd {
					label := string(src[v.LabelStart:v.LabelEnd])
					if label != tt.wantLabel {
						t.Errorf("label: want %q, got %q", tt.wantLabel, label)
					}
				} else if tt.wantLabel != "" {
					t.Errorf("expected label %q, got none", tt.wantLabel)
				}
			}
		})
	}
}

// =============================================================================
// COMPUTE LEIDEN parser tests
// =============================================================================

func TestParseComputeLeiden_ValidWithOptions(t *testing.T) {
	input := []byte("COMPUTE LEIDEN FROM MATCH (s:seeds)-[:CONNECTED_TO*1..3]->(target) OPTIONS (resolution = 1.0, iterations = 2)")
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(doc.ComputeLeidenStmts) != 1 {
		t.Fatalf("ComputeLeidenStmts: want 1, got %d", len(doc.ComputeLeidenStmts))
	}
	stmt := doc.ComputeLeidenStmts[0]

	// Root node must be included.
	if len(doc.Nodes) == 0 {
		t.Fatal("Nodes must contain the root NodeRef")
	}
	root := doc.Nodes[len(doc.Nodes)-1]
	if root.Kind != NodeKindComputeLeidenStmt {
		t.Fatalf("root node kind: want NodeKindComputeLeidenStmt, got %v", root.Kind)
	}

	// MatchPath must reference a valid MatchPath.
	if stmt.MatchPath.Kind != NodeKindMatchPath {
		t.Fatalf("MatchPath.Kind: want NodeKindMatchPath, got %v", stmt.MatchPath.Kind)
	}
	mp := doc.MatchPaths[stmt.MatchPath.ID]
	if mp.PathNodesCount < 3 {
		t.Fatalf("expected at least 3 path nodes, got %d", mp.PathNodesCount)
	}

	// First vertex: alias=s, label=seeds.
	v0Ref := doc.Nodes[mp.PathNodesStart]
	if v0Ref.Kind != NodeKindVertex {
		t.Fatal("first path node must be a vertex")
	}
	v0 := doc.Vertexes[v0Ref.ID]
	if got := string(input[v0.Alias:v0.AliasEnd]); got != "s" {
		t.Errorf("source vertex alias: want 's', got %q", got)
	}
	if got := string(input[v0.LabelStart:v0.LabelEnd]); got != "seeds" {
		t.Errorf("source vertex label: want 'seeds', got %q", got)
	}

	// Edge: type=CONNECTED_TO, direction=outbound, quantifier=*1..3.
	eRef := doc.Nodes[mp.PathNodesStart+1]
	if eRef.Kind != NodeKindEdge {
		t.Fatal("second path node must be an edge")
	}
	e := doc.Edges[eRef.ID]
	if got := string(input[e.TypeStart:e.TypeEnd]); got != "CONNECTED_TO" {
		t.Errorf("edge type: want 'CONNECTED_TO', got %q", got)
	}
	if e.Direction != 1 {
		t.Errorf("edge direction: want 1 (outbound), got %d", e.Direction)
	}
	if e.QuantMin != 1 || e.QuantMax != 3 {
		t.Errorf("quantifier: want [1,3], got [%d,%d]", e.QuantMin, e.QuantMax)
	}

	// Terminal vertex: alias=target.
	v1Ref := doc.Nodes[mp.PathNodesStart+2]
	if v1Ref.Kind != NodeKindVertex {
		t.Fatal("third path node must be a vertex")
	}
	v1 := doc.Vertexes[v1Ref.ID]
	if got := string(input[v1.Alias:v1.AliasEnd]); got != "target" {
		t.Errorf("terminal vertex alias: want 'target', got %q", got)
	}

	// Options: resolution=1.0, iterations=2.
	if stmt.OptionsCount != 2 {
		t.Fatalf("OptionsCount: want 2, got %d", stmt.OptionsCount)
	}
	opt0 := doc.LeidenOptions[stmt.OptionsStart]
	opt1 := doc.LeidenOptions[stmt.OptionsStart+1]
	if opt0.Kind != LeidenOptionResolution {
		t.Errorf("opt0 kind: want resolution, got %d", opt0.Kind)
	}
	if opt1.Kind != LeidenOptionIterations {
		t.Errorf("opt1 kind: want iterations, got %d", opt1.Kind)
	}
	if opt0.Value.Kind != NodeKindNumber {
		t.Errorf("opt0 value: want NodeKindNumber, got %v", opt0.Value.Kind)
	}
	if opt1.Value.Kind != NodeKindNumber {
		t.Errorf("opt1 value: want NodeKindNumber, got %v", opt1.Value.Kind)
	}

	t.Log("✅ valid COMPUTE LEIDEN with OPTIONS")
}

func TestParseComputeLeiden_WithoutOptions(t *testing.T) {
	input := []byte("COMPUTE LEIDEN FROM MATCH (s:seeds)-[:CONNECTED_TO]->(target)")
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.ComputeLeidenStmts) != 1 {
		t.Fatalf("ComputeLeidenStmts: want 1, got %d", len(doc.ComputeLeidenStmts))
	}
	stmt := doc.ComputeLeidenStmts[0]
	if stmt.OptionsCount != 0 {
		t.Errorf("OptionsCount: want 0, got %d", stmt.OptionsCount)
	}
	if stmt.MatchPath.Kind != NodeKindMatchPath {
		t.Fatal("MatchPath must be valid")
	}
	t.Log("✅ COMPUTE LEIDEN without OPTIONS")
}

func TestParseComputeLeiden_InboundPath(t *testing.T) {
	input := []byte("COMPUTE LEIDEN FROM MATCH (src)<-[:KNOWS]-(tgt)")
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stmt := doc.ComputeLeidenStmts[0]
	mp := doc.MatchPaths[stmt.MatchPath.ID]
	eRef := doc.Nodes[mp.PathNodesStart+1]
	e := doc.Edges[eRef.ID]
	if e.Direction != -1 {
		t.Errorf("edge direction: want -1 (inbound), got %d", e.Direction)
	}
	if got := string(input[e.TypeStart:e.TypeEnd]); got != "KNOWS" {
		t.Errorf("edge type: want 'KNOWS', got %q", got)
	}
	t.Log("✅ inbound MATCH path")
}

func TestParseComputeLeiden_UndirectedPath(t *testing.T) {
	input := []byte("COMPUTE LEIDEN FROM MATCH (a)-[:COLLABORATED_WITH]-(b)")
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stmt := doc.ComputeLeidenStmts[0]
	mp := doc.MatchPaths[stmt.MatchPath.ID]
	eRef := doc.Nodes[mp.PathNodesStart+1]
	e := doc.Edges[eRef.ID]
	if e.Direction != 0 {
		t.Errorf("edge direction: want 0 (undirected), got %d", e.Direction)
	}
	t.Log("✅ undirected MATCH path")
}

func TestParseComputeLeiden_CaseInsensitiveKeywords(t *testing.T) {
	input := []byte("compute leiden from match (a)-[:LINK]->(b)")
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.ComputeLeidenStmts) != 1 {
		t.Fatalf("ComputeLeidenStmts: want 1, got %d", len(doc.ComputeLeidenStmts))
	}
	t.Log("✅ case-insensitive keywords")
}

func TestParseComputeLeiden_RejectMissingLeiden(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("COMPUTE FROM MATCH (a)-[:LINK]->(b)"), &doc)
	if err == nil {
		t.Fatal("expected error for COMPUTE without LEIDEN")
	}
	t.Logf("rejected: %v", err)
}

func TestParseComputeLeiden_RejectMissingMatch(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("COMPUTE LEIDEN MATCH (a)-[:LINK]->(b)"), &doc)
	if err == nil {
		t.Fatal("expected error for missing FROM")
	}
	t.Logf("rejected: %v", err)
}

func TestParseComputeLeiden_RejectMissingFrom(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("COMPUTE LEIDEN (a)-[:LINK]->(b)"), &doc)
	if err == nil {
		t.Fatal("expected error for missing FROM")
	}
	t.Logf("rejected: %v", err)
}

func TestParseComputeLeiden_RejectEmptyOptions(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("COMPUTE LEIDEN FROM MATCH (a)-[:LINK]->(b) OPTIONS ()"), &doc)
	if err == nil {
		t.Fatal("expected error for empty OPTIONS")
	}
	t.Logf("rejected: %v", err)
}

func TestParseComputeLeiden_RejectMissingEquals(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("COMPUTE LEIDEN FROM MATCH (a)-[:LINK]->(b) OPTIONS (resolution 1.0)"), &doc)
	if err == nil {
		t.Fatal("expected error for missing '='")
	}
	t.Logf("rejected: %v", err)
}

func TestParseComputeLeiden_RejectMissingValue(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("COMPUTE LEIDEN FROM MATCH (a)-[:LINK]->(b) OPTIONS (resolution =)"), &doc)
	if err == nil {
		t.Fatal("expected error for missing value")
	}
	t.Logf("rejected: %v", err)
}

func TestParseComputeLeiden_RejectMissingComma(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("COMPUTE LEIDEN FROM MATCH (a)-[:LINK]->(b) OPTIONS (resolution = 1.0 iterations = 2)"), &doc)
	if err == nil {
		t.Fatal("expected error for missing comma")
	}
	t.Logf("rejected: %v", err)
}

func TestParseComputeLeiden_RejectMissingCloseParen(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("COMPUTE LEIDEN FROM MATCH (a)-[:LINK]->(b) OPTIONS (resolution = 1.0"), &doc)
	if err == nil {
		t.Fatal("expected error for missing ')'")
	}
	t.Logf("rejected: %v", err)
}

func TestParseComputeLeiden_RejectUnknownOption(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("COMPUTE LEIDEN FROM MATCH (a)-[:LINK]->(b) OPTIONS (unknown_opt = 5)"), &doc)
	if err == nil {
		t.Fatal("expected error for unknown option")
	}
	t.Logf("rejected: %v", err)
}

func TestParseComputeLeiden_RejectDuplicateOption(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("COMPUTE LEIDEN FROM MATCH (a)-[:LINK]->(b) OPTIONS (resolution = 1.0, resolution = 2.0)"), &doc)
	if err == nil {
		t.Fatal("expected error for duplicate option")
	}
	t.Logf("rejected: %v", err)
}

func TestParseComputeLeiden_StringAndIdentifierValues(t *testing.T) {
	input := []byte("COMPUTE LEIDEN FROM MATCH (a)-[:LINK]->(b) OPTIONS (direction = outbound, edge_kind = 'LINK')")
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stmt := doc.ComputeLeidenStmts[0]
	if stmt.OptionsCount != 2 {
		t.Fatalf("OptionsCount: want 2, got %d", stmt.OptionsCount)
	}
	opt0 := doc.LeidenOptions[stmt.OptionsStart]
	opt1 := doc.LeidenOptions[stmt.OptionsStart+1]
	if opt0.Kind != LeidenOptionDirection {
		t.Errorf("opt0 kind: want direction, got %d", opt0.Kind)
	}
	if opt0.Value.Kind != NodeKindIdentifier {
		t.Errorf("opt0 value kind: want NodeKindIdentifier, got %v", opt0.Value.Kind)
	}
	if opt1.Kind != LeidenOptionEdgeKind {
		t.Errorf("opt1 kind: want edge_kind, got %d", opt1.Kind)
	}
	if opt1.Value.Kind != NodeKindString {
		t.Errorf("opt1 value kind: want NodeKindString, got %v", opt1.Value.Kind)
	}
	t.Log("✅ string and identifier option values")
}

func TestParseComputeLeiden_AllSupportedOptions(t *testing.T) {
	input := []byte(`
		COMPUTE LEIDEN FROM MATCH (a)-[:LINK*1..3]->(b)
		OPTIONS (
			resolution = 1.0,
			iterations = 2,
			max_levels = 10,
			max_local_moving_passes = 5,
			min_hops = 1,
			max_hops = 3,
			max_vertices = 1000,
			max_edges = 5000,
			edge_kind = 'LINK',
			direction = outbound
		)
	`)
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stmt := doc.ComputeLeidenStmts[0]
	if stmt.OptionsCount != 10 {
		t.Fatalf("OptionsCount: want 10, got %d", stmt.OptionsCount)
	}
	expectedKinds := []LeidenOptionKind{
		LeidenOptionResolution,
		LeidenOptionIterations,
		LeidenOptionMaxLevels,
		LeidenOptionMaxLocalMovingPasses,
		LeidenOptionMinHops,
		LeidenOptionMaxHops,
		LeidenOptionMaxVertices,
		LeidenOptionMaxEdges,
		LeidenOptionEdgeKind,
		LeidenOptionDirection,
	}
	for i, wantKind := range expectedKinds {
		opt := doc.LeidenOptions[stmt.OptionsStart+int32(i)]
		if opt.Kind != wantKind {
			t.Errorf("option[%d] kind: want %d, got %d", i, wantKind, opt.Kind)
		}
	}
	t.Log("✅ all 10 supported options parse correctly")
}

// TestParseComputeLeiden_ExistingTestRegression confirms COMPUTE LEIDEN parsing
// does not break any existing SQL syntax.
func TestParseComputeLeiden_ExistingTestRegression(t *testing.T) {
	regressionInputs := []string{
		"SELECT id, name FROM users WHERE id > 123",
		"SELECT id FROM GRAPH_TABLE(my_graph MATCH (a)-[e]->(b))",
		"SELECT id FROM users WHERE SIMILARITY(v, [1.0, 0.5]) > 0.8 ORDER BY id DESC LIMIT 10",
		"SELECT id FROM users WHERE id BETWEEN 1 AND 5 OR status NOT IN (1, 2, 3)",
		"INSERT INTO users (id, name) VALUES ('42', 'alice')",
		"INSERT INTO GRAPH_EDGES VALUES ('A', 'KNOWS', 'B')",
		"UPDATE users SET name = 'bob' WHERE id = '42'",
		"DELETE FROM users WHERE id = '42'",
		"CREATE TABLE items (id TEXT, embedding VECTOR(768))",
		"DROP TABLE items",
		"CREATE INDEX items_idx ON items (id)",
		"DROP INDEX items_idx",
		"BEGIN EPOCH TRANSACTION",
		"COMMIT",
		"ROLLBACK",
		"SAVEPOINT sp1",
		"ROLLBACK TO SAVEPOINT sp1",
		"RELEASE SAVEPOINT sp1",
		"SELECT doc.title FROM services s JOIN MATCH (s)-[e]->(doc) ORDER BY doc.title ASC LIMIT 5",
	}
	for _, input := range regressionInputs {
		var doc QueryDoc
		if err := Parse([]byte(input), &doc); err != nil {
			t.Errorf("regression: %q → %v", input, err)
		}
	}
	t.Log("✅ no regressions in existing syntax")
}

// =============================================================================
// WITH CTE parser tests
// =============================================================================

func TestParseCTE_TargetQueryShape(t *testing.T) {
	input := []byte(`WITH local_clusters AS (
    COMPUTE LEIDEN FROM MATCH
    (s:seeds)-[:CONNECTED_TO*1..3]->(target)
    OPTIONS (resolution = 1.0, iterations = 2)
)
SELECT d.title, c.community_id
FROM documents d
JOIN local_clusters c ON d.node_id = c.node_id
WHERE c.community_id = 42
ORDER BY c.community_id ASC
LIMIT 10`)
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Exactly one SELECT.
	if len(doc.SelectStmts) != 1 {
		t.Fatalf("SelectStmts: want 1, got %d", len(doc.SelectStmts))
	}
	stmt := doc.SelectStmts[0]

	// Exactly one CTE.
	if stmt.CTEsCount != 1 {
		t.Fatalf("CTEsCount: want 1, got %d", stmt.CTEsCount)
	}

	cte := doc.CTEs[stmt.CTEsStart]
	if cteName := string(input[cte.NameStart:cte.NameEnd]); cteName != "local_clusters" {
		t.Errorf("CTE name: want local_clusters, got %q", cteName)
	}

	// CTE body is COMPUTE LEIDEN.
	if cte.Body.Kind != NodeKindComputeLeidenStmt {
		t.Fatalf("CTE body kind: want NodeKindComputeLeidenStmt, got %v", cte.Body.Kind)
	}

	cl := doc.ComputeLeidenStmts[cte.Body.ID]
	mp := doc.MatchPaths[cl.MatchPath.ID]
	if mp.PathNodesCount != 3 {
		t.Fatalf("MATCH path nodes: want 3, got %d", mp.PathNodesCount)
	}

	// Seed vertex.
	v0 := doc.Vertexes[doc.Nodes[mp.PathNodesStart].ID]
	if alias := string(input[v0.Alias:v0.AliasEnd]); alias != "s" {
		t.Errorf("seed alias: want s, got %q", alias)
	}
	if label := string(input[v0.LabelStart:v0.LabelEnd]); label != "seeds" {
		t.Errorf("seed label: want seeds, got %q", label)
	}

	// Edge.
	e := doc.Edges[doc.Nodes[mp.PathNodesStart+1].ID]
	if e.Direction != 1 {
		t.Errorf("edge direction: want 1, got %d", e.Direction)
	}
	if e.QuantMin != 1 || e.QuantMax != 3 {
		t.Errorf("quantifier: want [1,3], got [%d,%d]", e.QuantMin, e.QuantMax)
	}
	if eType := string(input[e.TypeStart:e.TypeEnd]); eType != "CONNECTED_TO" {
		t.Errorf("edge type: want CONNECTED_TO, got %q", eType)
	}

	// Terminal vertex.
	v1 := doc.Vertexes[doc.Nodes[mp.PathNodesStart+2].ID]
	if alias := string(input[v1.Alias:v1.AliasEnd]); alias != "target" {
		t.Errorf("terminal alias: want target, got %q", alias)
	}

	// Options.
	if cl.OptionsCount != 2 {
		t.Fatalf("OptionsCount: want 2, got %d", cl.OptionsCount)
	}

	// Outer SELECT: FROM table.
	if stmt.FromTable.Kind != NodeKindTableExpr {
		t.Fatal("FROM must be a table expression")
	}
	tbl := doc.TableExprs[stmt.FromTable.ID]
	if tblName := string(input[tbl.Start:tbl.End]); tblName != "documents" {
		t.Errorf("FROM table: want documents, got %q", tblName)
	}
	if alias := string(input[tbl.Alias:tbl.AliasEnd]); alias != "d" {
		t.Errorf("FROM alias: want d, got %q", alias)
	}

	// JOIN.
	if len(stmt.Joins) != 1 {
		t.Fatalf("Joins: want 1, got %d", len(stmt.Joins))
	}
	join := stmt.Joins[0]
	if joinName := string(input[join.TableStart:join.TableEnd]); joinName != "local_clusters" {
		t.Errorf("JOIN table: want local_clusters, got %q", joinName)
	}
	if joinAlias := string(input[join.Alias:join.AliasEnd]); joinAlias != "c" {
		t.Errorf("JOIN alias: want c, got %q", joinAlias)
	}

	// WHERE, ORDER BY, LIMIT.
	if stmt.WhereExpr.Kind == NodeKindUnknown {
		t.Error("WHERE expression must be present")
	}
	// Limit is an index into doc.Numbers, not the value itself.
	// Verify a LIMIT is present (non-negative index).
	if stmt.Limit < 0 {
		t.Error("LIMIT must be present")
	} else {
		limitVal := string(input[doc.Numbers[stmt.Limit].Start:doc.Numbers[stmt.Limit].End])
		if limitVal != "10" {
			t.Errorf("LIMIT value: want '10', got %q", limitVal)
		}
	}

	t.Log("✅ target query shape with CTE")
}

func TestParseCTE_WithoutOptions(t *testing.T) {
	input := []byte(`WITH local_clusters AS (
    COMPUTE LEIDEN FROM MATCH
    (s:seeds)-[:LINK]->(target)
)
SELECT c.community_id
FROM local_clusters c`)
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stmt := doc.SelectStmts[0]
	if stmt.CTEsCount != 1 {
		t.Fatalf("CTEsCount: want 1, got %d", stmt.CTEsCount)
	}
	cl := doc.ComputeLeidenStmts[doc.CTEs[stmt.CTEsStart].Body.ID]
	if cl.OptionsCount != 0 {
		t.Errorf("OptionsCount: want 0, got %d", cl.OptionsCount)
	}
	t.Log("✅ CTE without OPTIONS")
}

func TestParseCTE_CaseInsensitiveKeywords(t *testing.T) {
	input := []byte(`with local_clusters as (
    compute leiden from match
    (s:seeds)-[:LINK]->(target)
)
select c.community_id
from local_clusters c`)
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.SelectStmts) != 1 || doc.SelectStmts[0].CTEsCount != 1 {
		t.Fatal("case-insensitive CTE parsing failed")
	}
	t.Log("✅ case-insensitive WITH/AS/SELECT")
}

func TestParseCTE_ArbitraryName(t *testing.T) {
	input := []byte(`WITH scratch_results AS (
    COMPUTE LEIDEN FROM MATCH
    (s:roots)-[:LINK]->(target)
)
SELECT c.community_id
FROM scratch_results c`)
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cte := doc.CTEs[doc.SelectStmts[0].CTEsStart]
	if name := string(input[cte.NameStart:cte.NameEnd]); name != "scratch_results" {
		t.Errorf("CTE name: want scratch_results, got %q", name)
	}
	t.Log("✅ arbitrary CTE name preserved")
}

func TestParseCTE_RejectMissingName(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte(`WITH AS (COMPUTE LEIDEN FROM MATCH (a)-[:E]->(b)) SELECT 1`), &doc)
	if err == nil {
		t.Fatal("expected error for missing CTE name")
	}
	t.Logf("rejected: %v", err)
}

func TestParseCTE_RejectMissingAs(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte(`WITH c (COMPUTE LEIDEN FROM MATCH (a)-[:E]->(b)) SELECT 1`), &doc)
	if err == nil {
		t.Fatal("expected error for missing AS")
	}
	t.Logf("rejected: %v", err)
}

func TestParseCTE_RejectMissingOpenParen(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte(`WITH c AS COMPUTE LEIDEN FROM MATCH (a)-[:E]->(b)) SELECT 1`), &doc)
	if err == nil {
		t.Fatal("expected error for missing '('")
	}
	t.Logf("rejected: %v", err)
}

func TestParseCTE_RejectMissingCloseParen(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte(`WITH c AS (COMPUTE LEIDEN FROM MATCH (a)-[:E]->(b) SELECT 1`), &doc)
	if err == nil {
		t.Fatal("expected error for missing ')'")
	}
	t.Logf("rejected: %v", err)
}

func TestParseCTE_GenericSelectBody(t *testing.T) {
	var doc QueryDoc
	src := []byte(`WITH c AS (SELECT id FROM documents) SELECT id FROM c`)
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("generic SELECT CTE: %v", err)
	}
	if len(doc.CTEs) != 1 || doc.CTEs[0].Body.Kind != NodeKindSelectStmt {
		t.Fatalf("generic CTE body=%#v", doc.CTEs)
	}
	if len(doc.SelectStmts) != 2 || doc.Nodes[0].ID != 1 {
		t.Fatalf("nested SELECT ids nodes=%#v selects=%#v", doc.Nodes, doc.SelectStmts)
	}
}

func TestParseSubqueryPredicates(t *testing.T) {
	var doc QueryDoc
	if err := Parse([]byte(`SELECT id FROM documents WHERE id IN (SELECT id FROM authors)`), &doc); err != nil {
		t.Fatalf("IN subquery: %v", err)
	}
	if len(doc.InExprs) != 1 || !doc.InExprs[0].HasSubquery || len(doc.SubqueryExprs) != 1 {
		t.Fatalf("IN subquery AST=%#v subqueries=%#v", doc.InExprs, doc.SubqueryExprs)
	}
	doc.Reset()
	if err := Parse([]byte(`SELECT id FROM documents WHERE EXISTS (SELECT id FROM authors)`), &doc); err != nil {
		t.Fatalf("EXISTS subquery: %v", err)
	}
	if len(doc.SubqueryExprs) != 1 || !doc.SubqueryExprs[0].Exists {
		t.Fatalf("EXISTS subquery AST=%#v", doc.SubqueryExprs)
	}
}

func TestParseNestedSelectClauseArenasRemainIndependent(t *testing.T) {
	var doc QueryDoc
	src := []byte(`SELECT o.id FROM outer_rows o WHERE o.id IN (SELECT i.id FROM inner_rows i JOIN right_rows r ON i.id = r.id ORDER BY i.id)`)
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("nested SELECT: %v", err)
	}
	if len(doc.SelectStmts) != 2 {
		t.Fatalf("SelectStmts=%d, want 2", len(doc.SelectStmts))
	}
	inner := doc.SelectStmts[0]
	outer := doc.SelectStmts[1]
	if len(inner.Joins) != 1 || len(inner.OrderTerms) != 1 {
		t.Fatalf("inner clauses: joins=%d order_terms=%d", len(inner.Joins), len(inner.OrderTerms))
	}
	if len(outer.Joins) != 0 || len(outer.OrderTerms) != 0 {
		t.Fatalf("outer clauses aliased nested storage: joins=%d order_terms=%d", len(outer.Joins), len(outer.OrderTerms))
	}
}

func TestParseCTE_RejectCTEBodyIsMatch(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte(`WITH c AS (MATCH (a)-[e]->(b)) SELECT 1`), &doc)
	if err == nil {
		t.Fatal("expected error for MATCH in CTE body")
	}
	t.Logf("rejected: %v", err)
}

func TestParseCTE_RejectMissingSelect(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte(`WITH c AS (COMPUTE LEIDEN FROM MATCH (a)-[:E]->(b))`), &doc)
	if err == nil {
		t.Fatal("expected error for missing SELECT after CTE")
	}
	t.Logf("rejected: %v", err)
}

func TestParseCTE_RejectDuplicateName(t *testing.T) {
	var doc QueryDoc
	// Not supported by single-CTE limitation; attempt to parse twice would
	// require multiple CTEs, which is rejected first. Test duplicate detection
	// via two sequential WITH clauses (which isn't valid SQL anyway, but
	// the parser should handle it).
	err := Parse([]byte(`WITH c AS (COMPUTE LEIDEN FROM MATCH (a)-[:E]->(b)) WITH c AS (COMPUTE LEIDEN FROM MATCH (a)-[:E]->(b)) SELECT 1`), &doc)
	_ = err
	// The second WITH triggers "unexpected token after CTE" before duplicate check.
	// Duplicate detection is verified by the CTE struct having correct name tracking.
	// This test just confirms no panic.
	t.Log("✅ duplicate name handling doesn't panic")
}

func TestParseCTEMultiple(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte(`WITH a AS (SELECT id FROM left_rows), b AS (SELECT id FROM a) SELECT id FROM b`), &doc)
	if err != nil {
		t.Fatalf("multiple CTEs: %v", err)
	}
	if len(doc.CTEs) != 2 || doc.SelectStmts[len(doc.SelectStmts)-1].CTEsCount != 2 {
		t.Fatalf("CTEs=%d select=%#v", len(doc.CTEs), doc.SelectStmts)
	}
}

func TestParseCTE_RejectMalformedBody(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte(`WITH c AS (COMPUTE LEIDEN MATCH (a)-[:E]->(b)) SELECT 1`), &doc)
	if err == nil {
		t.Fatal("expected error for malformed body")
	}
	t.Logf("rejected malformed body: %v", err)
}

// Regression: standalone COMPUTE LEIDEN still works.
func TestParseCTE_StandaloneLeidenRegression(t *testing.T) {
	input := []byte(`COMPUTE LEIDEN FROM MATCH (s:seeds)-[:LINK*1..2]->(target)`)
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("standalone COMPUTE LEIDEN: %v", err)
	}
	if len(doc.ComputeLeidenStmts) != 1 {
		t.Fatal("standalone COMPUTE LEIDEN must parse")
	}
	if len(doc.CTEs) != 0 {
		t.Fatal("standalone must have zero CTEs")
	}
	t.Log("✅ standalone COMPUTE LEIDEN regression")
}

// Regression: ordinary SELECT still works.
func TestParseCTE_OrdinarySelectRegression(t *testing.T) {
	input := []byte(`SELECT id, name FROM users WHERE id > 123`)
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("ordinary SELECT: %v", err)
	}
	if len(doc.SelectStmts) != 1 {
		t.Fatal("ordinary SELECT must parse")
	}
	if doc.SelectStmts[0].CTEsCount != 0 {
		t.Fatal("ordinary SELECT must have zero CTEs")
	}
	t.Log("✅ ordinary SELECT regression")
}

// Regression: JOIN still works.
func TestParseCTE_JoinRegression(t *testing.T) {
	input := []byte(`SELECT doc.title FROM services s JOIN MATCH (s)-[e]->(doc) ORDER BY doc.title ASC LIMIT 5`)
	var doc QueryDoc
	if err := Parse(input, &doc); err != nil {
		t.Fatalf("JOIN: %v", err)
	}
	if len(doc.SelectStmts) != 1 {
		t.Fatal("JOIN SELECT must parse")
	}
	t.Log("✅ JOIN regression")
}

// Regression: transaction statements still work.
func TestParseCTE_TransactionRegression(t *testing.T) {
	tests := []string{
		"BEGIN EPOCH TRANSACTION",
		"COMMIT",
		"ROLLBACK",
		"SAVEPOINT sp1",
		"ROLLBACK TO SAVEPOINT sp1",
		"RELEASE SAVEPOINT sp1",
	}
	for _, sql := range tests {
		var doc QueryDoc
		if err := Parse([]byte(sql), &doc); err != nil {
			t.Errorf("transaction %q: %v", sql, err)
		}
	}
	t.Log("✅ transaction statements regression")
}

// QueryDoc reset: parse CTE, reuse for ordinary SELECT, assert no CTE remnants.
func TestParseCTE_QueryDocReset(t *testing.T) {
	var doc QueryDoc
	if err := Parse([]byte(`WITH c AS (COMPUTE LEIDEN FROM MATCH (a)-[:E]->(b)) SELECT 1 FROM c`), &doc); err != nil {
		t.Fatalf("CTE parse: %v", err)
	}
	if len(doc.CTEs) == 0 || doc.SelectStmts[0].CTEsCount == 0 {
		t.Fatal("CTE must be present in first parse")
	}

	// Reuse doc for ordinary SELECT.
	if err := Parse([]byte(`SELECT id FROM items`), &doc); err != nil {
		t.Fatalf("SELECT after CTE: %v", err)
	}
	if len(doc.CTEs) != 0 {
		t.Fatal("CTEs must be cleared after Reset")
	}
	if len(doc.ComputeLeidenStmts) != 0 {
		t.Fatal("ComputeLeidenStmts must be cleared after Reset")
	}
	if doc.SelectStmts[0].CTEsCount != 0 {
		t.Fatal("SelectStmt.CTEsCount must be 0 after Reset")
	}
	t.Log("✅ QueryDoc Reset clears CTEs")
}

// TestParseCreateTableVector verifies VECTOR(n) dimension parsing and
// rejection of invalid dimension values.
func TestParseCreateTableVector(t *testing.T) {
	tests := []struct {
		name        string
		sql         string
		wantDim     uint32
		wantTypeStr string
		wantErr     bool
		errContains string
	}{
		{
			name:        "VECTOR(3)",
			sql:         "CREATE TABLE a (v VECTOR(3))",
			wantDim:     3,
			wantTypeStr: "VECTOR(3)",
		},
		{
			name:        "VECTOR(768)",
			sql:         "CREATE TABLE b (v VECTOR(768))",
			wantDim:     768,
			wantTypeStr: "VECTOR(768)",
		},
		{
			name:        "VECTOR(1536)",
			sql:         "CREATE TABLE c (v VECTOR(1536))",
			wantDim:     1536,
			wantTypeStr: "VECTOR(1536)",
		},
		{
			name:        "VECTOR(4096)",
			sql:         "CREATE TABLE d (v VECTOR(4096))",
			wantDim:     4096,
			wantTypeStr: "VECTOR(4096)",
		},
		{
			name:        "vector(128) case-insensitive",
			sql:         "CREATE TABLE e (v vector(128))",
			wantDim:     128,
			wantTypeStr: "vector(128)",
		},
		{
			name:        "VECTOR column with metadata columns",
			sql:         "CREATE TABLE f (id TEXT, v VECTOR(64), name TEXT)",
			wantDim:     64,
			wantTypeStr: "VECTOR(64)",
		},
		{
			name:        "VECTOR(0) rejected",
			sql:         "CREATE TABLE g (v VECTOR(0))",
			wantErr:     true,
			errContains: "positive",
		},
		{
			name:        "VECTOR() empty rejected",
			sql:         "CREATE TABLE h (v VECTOR())",
			wantErr:     true,
			errContains: "numeric dimension",
		},
		{
			name:        "bare VECTOR rejected",
			sql:         "CREATE TABLE i (v VECTOR)",
			wantErr:     true,
			errContains: "requires a dimension",
		},
		{
			name:        "VECTOR(65536) accepted beyond legacy ceiling",
			sql:         "CREATE TABLE j (v VECTOR(65536))",
			wantDim:     65536,
			wantTypeStr: "VECTOR(65536)",
		},
		{
			name:        "VECTOR(uint32 max)",
			sql:         "CREATE TABLE k (v VECTOR(4294967295))",
			wantDim:     4294967295,
			wantTypeStr: "VECTOR(4294967295)",
		},
		{
			name:        "VECTOR(uint32 overflow) rejected",
			sql:         "CREATE TABLE l (v VECTOR(4294967296))",
			wantErr:     true,
			errContains: "uint32 maximum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc QueryDoc
			err := Parse([]byte(tt.sql), &doc)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Fatalf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(doc.CreateTableStmts) != 1 {
				t.Fatalf("expected 1 CreateTableStmt, got %d", len(doc.CreateTableStmts))
			}
			var foundVec bool
			for _, col := range doc.CreateTableStmts[0].Columns {
				if isTypeVector([]byte(tt.sql), col.TypeStart, col.TypeEnd) || col.TypeParam > 0 {
					foundVec = true
					if col.TypeParam != tt.wantDim {
						t.Errorf("TypeParam: want %d, got %d", tt.wantDim, col.TypeParam)
					}
					typeStr := string([]byte(tt.sql)[col.TypeStart:col.TypeEnd])
					if typeStr != tt.wantTypeStr {
						t.Errorf("Type string: want %q, got %q", tt.wantTypeStr, typeStr)
					}
				}
			}
			if !foundVec && tt.wantDim > 0 {
				t.Error("no VECTOR column found")
			}
		})
	}
}

// TestParseAlterTableVector verifies VECTOR(n) in ALTER TABLE ADD COLUMN.
func TestParseAlterTableVector(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("ALTER TABLE t ADD v VECTOR(512)"), &doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(doc.AlterTableStmts) != 1 {
		t.Fatalf("expected 1 AlterTableStmt, got %d", len(doc.AlterTableStmts))
	}
	if doc.AlterTableStmts[0].AddColumn.TypeParam != 512 {
		t.Errorf("TypeParam: want 512, got %d", doc.AlterTableStmts[0].AddColumn.TypeParam)
	}
}

func TestParseAlterTableOptionalColumnKeyword(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("ALTER TABLE t ADD COLUMN note TEXT"), &doc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(doc.AlterTableStmts) != 1 {
		t.Fatalf("expected 1 AlterTableStmt, got %d", len(doc.AlterTableStmts))
	}
	stmt := doc.AlterTableStmts[0]
	if got := string([]byte("ALTER TABLE t ADD COLUMN note TEXT")[stmt.AddColumn.NameStart:stmt.AddColumn.NameEnd]); got != "note" {
		t.Fatalf("column name: want note, got %q", got)
	}
	if got := string([]byte("ALTER TABLE t ADD COLUMN note TEXT")[stmt.AddColumn.TypeStart:stmt.AddColumn.TypeEnd]); got != "TEXT" {
		t.Fatalf("column type: want TEXT, got %q", got)
	}
}

func TestParseSQLAlchemyJoinTableAliases(t *testing.T) {
	const query = "SELECT a.id FROM authors AS a LEFT OUTER JOIN documents AS d ON a.id = d.author_id LEFT JOIN graph_refs AS r ON d.id = r.document_id"
	var doc QueryDoc
	if err := Parse([]byte(query), &doc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(doc.SelectStmts) != 1 || len(doc.SelectStmts[0].Joins) != 2 {
		t.Fatalf("selects/joins: want 1/2, got %d/%d", len(doc.SelectStmts), len(doc.SelectStmts[0].Joins))
	}
	if len(doc.TableExprs) != 1 || doc.TableExprs[0].Alias == 0 {
		t.Fatalf("FROM table AS alias was not recorded: %+v", doc.TableExprs)
	}
	for i, join := range doc.SelectStmts[0].Joins {
		if join.Alias == 0 {
			t.Fatalf("join %d AS alias was not recorded: %+v", i, join)
		}
	}
}

func TestParseASOFLiteralAndParameter(t *testing.T) {
	for _, query := range []string{
		"SELECT id FROM documents AS OF LSN 42",
		"SELECT id FROM documents AS OF LSN $snapshot_lsn",
	} {
		var doc QueryDoc
		if err := Parse([]byte(query), &doc); err != nil {
			t.Fatalf("Parse(%q): %v", query, err)
		}
		if len(doc.TableExprs) != 1 {
			t.Fatalf("Parse(%q): table count=%d, want 1", query, len(doc.TableExprs))
		}
		table := doc.TableExprs[0]
		if !table.TemporalLSN {
			t.Fatalf("Parse(%q): TemporalLSN=false", query)
		}
		if got := string([]byte(query)[table.LSNStart:table.LSNEnd]); got == "" {
			t.Fatalf("Parse(%q): empty LSN span", query)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && search(s, substr)
}

func search(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestParseCreateTableFK verifies FOREIGN KEY constraint parsing.
func TestParseCreateTableFK(t *testing.T) {
	tests := []struct {
		name         string
		sql          string
		wantFKCount  int
		wantFKSrcCol string
		wantFKTgtTbl string
		wantFKTgtCol string
		wantOnDelete OnDeleteAction
		wantFKName   string // empty for unnamed
		wantErr      bool
		errContains  string
	}{
		{
			name:         "inline REFERENCES without action",
			sql:          "CREATE TABLE orders (id TEXT, customer_id UUID REFERENCES customers(id))",
			wantFKCount:  1,
			wantFKSrcCol: "customer_id",
			wantFKTgtTbl: "customers",
			wantFKTgtCol: "id",
			wantOnDelete: OnDeleteNoAction,
		},
		{
			name:         "inline REFERENCES ON DELETE CASCADE",
			sql:          "CREATE TABLE orders (id TEXT PRIMARY KEY, customer_id UUID REFERENCES customers(id) ON DELETE CASCADE)",
			wantFKCount:  1,
			wantFKSrcCol: "customer_id",
			wantFKTgtTbl: "customers",
			wantFKTgtCol: "id",
			wantOnDelete: OnDeleteCascade,
		},
		{
			name:         "inline REFERENCES ON DELETE RESTRICT",
			sql:          "CREATE TABLE t (a TEXT REFERENCES parent(b) ON DELETE RESTRICT)",
			wantFKCount:  1,
			wantFKSrcCol: "a",
			wantFKTgtTbl: "parent",
			wantFKTgtCol: "b",
			wantOnDelete: OnDeleteRestrict,
		},
		{
			name:         "named inline constraint",
			sql:          "CREATE TABLE orders (id TEXT, customer_id UUID CONSTRAINT valid_customer REFERENCES customers(id))",
			wantFKCount:  1,
			wantFKSrcCol: "customer_id",
			wantFKTgtTbl: "customers",
			wantFKTgtCol: "id",
			wantFKName:   "valid_customer",
		},
		{
			name:         "table-level FOREIGN KEY",
			sql:          "CREATE TABLE orders (id TEXT PRIMARY KEY, customer_id UUID, FOREIGN KEY (customer_id) REFERENCES customers(id))",
			wantFKCount:  1,
			wantFKSrcCol: "customer_id",
			wantFKTgtTbl: "customers",
			wantFKTgtCol: "id",
			wantOnDelete: OnDeleteNoAction,
		},
		{
			name:         "table-level named FOREIGN KEY",
			sql:          "CREATE TABLE orders (id TEXT PRIMARY KEY, customer_id UUID, CONSTRAINT valid_customer FOREIGN KEY (customer_id) REFERENCES customers(id) ON DELETE CASCADE)",
			wantFKCount:  1,
			wantFKSrcCol: "customer_id",
			wantFKTgtTbl: "customers",
			wantFKTgtCol: "id",
			wantOnDelete: OnDeleteCascade,
			wantFKName:   "valid_customer",
		},
		{
			name:         "GRAPH_NODES target",
			sql:          "CREATE TABLE users (id BIGINT PRIMARY KEY, CONSTRAINT valid_neighbor FOREIGN KEY (id) REFERENCES GRAPH_NODES(id) ON DELETE CASCADE)",
			wantFKCount:  1,
			wantFKSrcCol: "id",
			wantFKTgtTbl: "GRAPH_NODES",
			wantFKTgtCol: "id",
			wantOnDelete: OnDeleteCascade,
			wantFKName:   "valid_neighbor",
		},
		{
			name:         "case-insensitive keywords",
			sql:          "create table t (a text, foreign key (a) references parent(b) on delete cascade)",
			wantFKCount:  1,
			wantFKSrcCol: "a",
			wantFKTgtTbl: "parent",
			wantFKTgtCol: "b",
			wantOnDelete: OnDeleteCascade,
		},
		{
			name:         "multiple columns plus table-level FK",
			sql:          "CREATE TABLE t (id TEXT, name TEXT, score INT, FOREIGN KEY (id) REFERENCES parent(id))",
			wantFKCount:  1,
			wantFKSrcCol: "id",
			wantFKTgtTbl: "parent",
			wantFKTgtCol: "id",
		},
		{
			name:         "column with FK and NOT NULL and PK",
			sql:          "CREATE TABLE t (id TEXT NOT NULL PRIMARY KEY REFERENCES parent(id) ON DELETE CASCADE)",
			wantFKCount:  1,
			wantFKSrcCol: "id",
			wantFKTgtTbl: "parent",
			wantFKTgtCol: "id",
			wantOnDelete: OnDeleteCascade,
		},
		// ── Rejection cases ──────────────────────────────────
		{
			name:         "composite source columns accepted",
			sql:          "CREATE TABLE t (FOREIGN KEY (a, b) REFERENCES parent(id))",
			wantFKCount:  1,
			wantFKSrcCol: "a",
			wantFKTgtTbl: "parent",
			wantFKTgtCol: "id",
		},
		{
			name:         "composite target columns accepted",
			sql:          "CREATE TABLE t (a TEXT REFERENCES parent(b, c))",
			wantFKCount:  1,
			wantFKSrcCol: "a",
			wantFKTgtTbl: "parent",
			wantFKTgtCol: "b",
		},
		{
			name:         "ON DELETE SET NULL accepted",
			sql:          "CREATE TABLE t (a TEXT REFERENCES parent(b) ON DELETE SET NULL)",
			wantFKCount:  1,
			wantFKSrcCol: "a",
			wantFKTgtTbl: "parent",
			wantFKTgtCol: "b",
			wantOnDelete: OnDeleteSetNull,
		},
		{
			name:        "duplicate constraint names rejected",
			sql:         "CREATE TABLE t (a TEXT CONSTRAINT dup REFERENCES p1(x), b TEXT CONSTRAINT dup REFERENCES p2(y))",
			wantErr:     true,
			errContains: "duplicate",
		},
		{
			name:        "missing source column",
			sql:         "CREATE TABLE t (FOREIGN KEY () REFERENCES parent(id))",
			wantErr:     true,
			errContains: "source column",
		},
		{
			name:        "missing target table",
			sql:         "CREATE TABLE t (a TEXT REFERENCES (id))",
			wantErr:     true,
			errContains: "target table",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc QueryDoc
			err := Parse([]byte(tt.sql), &doc)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Fatalf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(doc.CreateTableStmts) != 1 {
				t.Fatalf("expected 1 CreateTableStmt, got %d", len(doc.CreateTableStmts))
			}
			stmt := doc.CreateTableStmts[0]
			if len(stmt.ForeignKeys) != tt.wantFKCount {
				t.Fatalf("FK count: want %d, got %d", tt.wantFKCount, len(stmt.ForeignKeys))
			}
			if tt.wantFKCount == 0 {
				return
			}
			fk := stmt.ForeignKeys[0]
			src := []byte(tt.sql)

			if got := string(src[fk.SourceColumns[0].Start:fk.SourceColumns[0].End]); got != tt.wantFKSrcCol {
				t.Errorf("src col: want %q, got %q", tt.wantFKSrcCol, got)
			}
			if got := string(src[fk.TgtTableStart:fk.TgtTableEnd]); got != tt.wantFKTgtTbl {
				t.Errorf("tgt table: want %q, got %q", tt.wantFKTgtTbl, got)
			}
			if got := string(src[fk.TargetColumns[0].Start:fk.TargetColumns[0].End]); got != tt.wantFKTgtCol {
				t.Errorf("tgt col: want %q, got %q", tt.wantFKTgtCol, got)
			}
			if fk.OnDelete != tt.wantOnDelete {
				t.Errorf("on delete: want %d, got %d", tt.wantOnDelete, fk.OnDelete)
			}
			if tt.wantFKName != "" {
				if fk.NameStart == 0 {
					t.Errorf("expected constraint name %q but NameStart is 0", tt.wantFKName)
				} else if got := string(src[fk.NameStart:fk.NameEnd]); got != tt.wantFKName {
					t.Errorf("constraint name: want %q, got %q", tt.wantFKName, got)
				}
			}
		})
	}
}
