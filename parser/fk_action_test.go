package parser

import "testing"

func TestParseForeignKeyNoAction(t *testing.T) {
	var doc QueryDoc
	err := Parse([]byte("CREATE TABLE child (id TEXT, parent_id TEXT REFERENCES parent(id) ON DELETE NO ACTION)"), &doc)
	if err != nil {
		t.Fatalf("parse explicit NO ACTION: %v", err)
	}
	if len(doc.CreateTableStmts) != 1 || len(doc.CreateTableStmts[0].ForeignKeys) != 1 {
		t.Fatalf("foreign keys = %d, want 1", len(doc.CreateTableStmts[0].ForeignKeys))
	}
	if doc.CreateTableStmts[0].ForeignKeys[0].OnDelete != OnDeleteNoAction {
		t.Fatalf("OnDelete = %d, want NO ACTION", doc.CreateTableStmts[0].ForeignKeys[0].OnDelete)
	}
}
