package parser

import "testing"

func TestParseSetOperations(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		op   SetOperation
		all  bool
	}{
		{"union", "SELECT id FROM left_rows UNION SELECT id FROM right_rows", SetOpUnion, false},
		{"union all", "SELECT id FROM left_rows UNION ALL SELECT id FROM right_rows", SetOpUnion, true},
		{"intersect", "SELECT id FROM left_rows INTERSECT SELECT id FROM right_rows", SetOpIntersect, false},
		{"intersect all", "SELECT id FROM left_rows INTERSECT ALL SELECT id FROM right_rows", SetOpIntersect, true},
		{"except", "SELECT id FROM left_rows EXCEPT SELECT id FROM right_rows", SetOpExcept, false},
		{"except all", "SELECT id FROM left_rows EXCEPT ALL SELECT id FROM right_rows", SetOpExcept, true},
	}
	for _, tc := range cases {
		doc := &QueryDoc{}
		if err := Parse([]byte(tc.sql), doc); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		found := false
		for _, stmt := range doc.SelectStmts {
			if stmt.SetOp != tc.op {
				continue
			}
			found = true
			if stmt.SetOpAll != tc.all || stmt.UnionNext.Kind != NodeKindSelectStmt {
				t.Fatalf("%s: stmt=%#v", tc.name, stmt)
			}
		}
		if !found {
			t.Fatalf("%s: set operation not found in %#v", tc.name, doc.SelectStmts)
		}
	}
}
