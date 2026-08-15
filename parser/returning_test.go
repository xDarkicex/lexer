package parser

import "testing"

func TestParseDMLReturningColumnsAndStar(t *testing.T) {
	var doc QueryDoc
	src := []byte("INSERT INTO docs (id, title) VALUES ('d1', 'one') RETURNING id, title")
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("INSERT RETURNING parse: %v", err)
	}
	if len(doc.InsertStmts) != 1 || len(doc.InsertStmts[0].Returning) != 2 || doc.InsertStmts[0].ReturningStar {
		t.Fatalf("insert returning=%#v", doc.InsertStmts)
	}
	if got := string(src[doc.Identifiers[doc.InsertStmts[0].Returning[1].ID].Start:doc.Identifiers[doc.InsertStmts[0].Returning[1].ID].End]); got != "title" {
		t.Fatalf("returning column=%q, want title", got)
	}

	doc.Reset()
	if err := Parse([]byte("UPDATE docs SET title = 'two' WHERE id = 'd1' RETURNING *"), &doc); err != nil {
		t.Fatalf("UPDATE RETURNING parse: %v", err)
	}
	if len(doc.UpdateStmts) != 1 || !doc.UpdateStmts[0].ReturningStar {
		t.Fatalf("update returning star=%#v", doc.UpdateStmts)
	}

	doc.Reset()
	if err := Parse([]byte("DELETE FROM docs WHERE id = 'd1' RETURNING id"), &doc); err != nil {
		t.Fatalf("DELETE RETURNING parse: %v", err)
	}
	if len(doc.DeleteStmts) != 1 || len(doc.DeleteStmts[0].Returning) != 1 {
		t.Fatalf("delete returning=%#v", doc.DeleteStmts)
	}
}

func TestParseDMLReturningRejectsMalformedList(t *testing.T) {
	for _, src := range []string{
		"INSERT INTO docs (id) VALUES ('d1') RETURNING",
		"UPDATE docs SET title = 'x' WHERE id = 'd1' RETURNING id,",
		"DELETE FROM docs WHERE id = 'd1' RETURNING 1",
	} {
		var doc QueryDoc
		if err := Parse([]byte(src), &doc); err == nil {
			t.Errorf("Parse(%q) unexpectedly succeeded", src)
		}
	}
}
