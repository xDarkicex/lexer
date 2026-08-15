package parser

import "testing"

func TestParseSignedAndScientificNumericLiterals(t *testing.T) {
	for _, sql := range []string{
		"INSERT INTO metrics (id, value) VALUES ('m1', -5.0e-3)",
		"INSERT INTO metrics (id, value) VALUES ('m1', +1e5)",
	} {
		doc := &QueryDoc{}
		if err := Parse([]byte(sql), doc); err != nil {
			t.Fatalf("Parse(%q): %v", sql, err)
		}
		if len(doc.InsertStmts) != 1 || len(doc.InsertStmts[0].Values) != 2 {
			t.Fatalf("unexpected insert AST for %q: %#v", sql, doc.InsertStmts)
		}
		ref := doc.InsertStmts[0].Values[1]
		if ref.Kind != NodeKindNumber {
			t.Fatalf("value kind=%v want number", ref.Kind)
		}
		n := doc.Numbers[ref.ID]
		if got := sql[n.Start:n.End]; got != "-5.0e-3" && got != "+1e5" {
			t.Fatalf("number span=%q", got)
		}
	}
}

func TestParseSignedDefaultNumericLiteral(t *testing.T) {
	doc := &QueryDoc{}
	if err := Parse([]byte("CREATE TABLE metrics (value FLOAT DEFAULT -1.5e-3)"), doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.CreateTableStmts) != 1 {
		t.Fatalf("create statements=%d", len(doc.CreateTableStmts))
	}
}

func TestParseRejectsMalformedScientificLiteral(t *testing.T) {
	doc := &QueryDoc{}
	if err := Parse([]byte("INSERT INTO metrics (id, value) VALUES ('bad', 1e)"), doc); err == nil {
		t.Fatal("malformed scientific literal was accepted")
	}
}
