package parser

import (
	"testing"

	"github.com/xDarkicex/lexer"
)

func TestParseInsertOnConflictDoNothing(t *testing.T) {
	var doc QueryDoc
	if err := Parse([]byte("INSERT INTO docs (id, title) VALUES ('d1', 'one') ON CONFLICT (id) DO NOTHING"), &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.InsertStmts) != 1 {
		t.Fatalf("InsertStmts=%d, want 1", len(doc.InsertStmts))
	}
	stmt := doc.InsertStmts[0]
	if stmt.ConflictAction != 1 || len(stmt.ConflictColumns) != 1 {
		t.Fatalf("conflict action/target = %d/%d", stmt.ConflictAction, len(stmt.ConflictColumns))
	}
	ref := doc.Identifiers[stmt.ConflictColumns[0].ID]
	if got := string([]byte("INSERT INTO docs (id, title) VALUES ('d1', 'one') ON CONFLICT (id) DO NOTHING")[ref.Start:ref.End]); got != "id" {
		t.Fatalf("conflict target=%q, want id", got)
	}
}

func TestParseInsertOnConflictExcludedUpdate(t *testing.T) {
	var doc QueryDoc
	src := []byte("INSERT INTO docs (id, title, category) VALUES ('d1', 'one', 'a') ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, category = 'b'")
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stmt := doc.InsertStmts[0]
	if stmt.ConflictAction != 2 || len(stmt.ConflictSet) != 2 {
		t.Fatalf("conflict action/set = %d/%d", stmt.ConflictAction, len(stmt.ConflictSet))
	}
	if stmt.ConflictSet[0].ExcludedColumn.Kind != NodeKindIdentifier {
		t.Fatalf("first assignment did not preserve EXCLUDED.column: %#v", stmt.ConflictSet[0])
	}
	if stmt.ConflictSet[1].Value.Kind != NodeKindString {
		t.Fatalf("second assignment value kind=%v, want string", stmt.ConflictSet[1].Value.Kind)
	}
}

func TestParseInsertOnConflictExpressionUpdate(t *testing.T) {
	var doc QueryDoc
	src := []byte("INSERT INTO counters (id, counter) VALUES ('c1', 2) ON CONFLICT (id) DO UPDATE SET counter = counter + EXCLUDED.counter")
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	stmt := doc.InsertStmts[0]
	if len(stmt.ConflictSet) != 1 || stmt.ConflictSet[0].Value.Kind != NodeKindBinaryExpr {
		t.Fatalf("conflict assignment=%#v, want binary expression", stmt.ConflictSet)
	}
	be := doc.BinaryExprs[stmt.ConflictSet[0].Value.ID]
	if be.Left.Kind != NodeKindIdentifier || be.Right.Kind != NodeKindIdentifier {
		t.Fatalf("binary operands=%#v, want identifiers", be)
	}
	if doc.Identifiers[be.Right.ID].ResolvedKind != ResolvedKindExcluded {
		t.Fatalf("right operand resolved kind=%d, want EXCLUDED", doc.Identifiers[be.Right.ID].ResolvedKind)
	}
}

func TestParseInsertOnConflictRejectsMalformed(t *testing.T) {
	cases := []string{
		"INSERT INTO docs (id) VALUES ('d1') ON CONFLICT (id) DO",
		"INSERT INTO docs (id) VALUES ('d1') ON CONFLICT (id) DO UPDATE",
		"INSERT INTO docs (id) VALUES ('d1') ON CONFLICT (id) DO UPDATE SET title",
		"INSERT INTO docs (id) VALUES ('d1') ON CONFLICT () DO NOTHING",
		"INSERT INTO docs (id) VALUES ('d1') ON CONFLICT (id) DO NOTHING unexpected",
	}
	for _, src := range cases {
		var doc QueryDoc
		if err := Parse([]byte(src), &doc); err == nil {
			t.Errorf("Parse(%q) unexpectedly succeeded", src)
		}
	}
}

func TestLexerOnConflictKeywords(t *testing.T) {
	s := lexer.New([]byte("ON CONFLICT DO NOTHING EXCLUDED"))
	want := []lexer.Kind{lexer.KindOn, lexer.KindConflict, lexer.KindDo, lexer.KindNothing, lexer.KindExcluded}
	for i, expected := range want {
		var tok lexer.Token
		var ok bool
		for {
			tok, ok = s.Next()
			if !ok || tok.Kind != lexer.KindWhitespace {
				break
			}
		}
		if !ok || tok.Kind != expected {
			t.Fatalf("token %d = %#v/%v, want kind %v", i, tok, ok, expected)
		}
	}
}
