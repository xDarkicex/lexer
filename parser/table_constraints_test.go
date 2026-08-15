package parser

import "testing"

func TestParseTableLevelCompositePrimaryKey(t *testing.T) {
	src := []byte(`CREATE TABLE memberships (
        tenant_id TEXT,
        user_id UUID,
        PRIMARY KEY (tenant_id, user_id)
    )`)
	var doc QueryDoc
	err := Parse(src, &doc)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.CreateTableStmts) != 1 {
		t.Fatalf("CreateTableStmts=%d, want 1", len(doc.CreateTableStmts))
	}
	pk := doc.CreateTableStmts[0].PrimaryKey
	if pk == nil {
		t.Fatal("missing table-level primary key")
	}
	if len(pk.Columns) != 2 {
		t.Fatalf("primary key columns=%d, want 2", len(pk.Columns))
	}
	if got := string(src[pk.Columns[0].Start:pk.Columns[0].End]); got != "tenant_id" {
		t.Fatalf("first PK column=%q", got)
	}
	if got := string(src[pk.Columns[1].Start:pk.Columns[1].End]); got != "user_id" {
		t.Fatalf("second PK column=%q", got)
	}
}

func TestParseNamedTableLevelCompositePrimaryKey(t *testing.T) {
	src := []byte(`CREATE TABLE memberships (
        tenant_id TEXT,
        user_id UUID,
        CONSTRAINT memberships_pk PRIMARY KEY (tenant_id, user_id)
    )`)
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pk := doc.CreateTableStmts[0].PrimaryKey
	if pk == nil || len(pk.Columns) != 2 {
		t.Fatalf("unexpected primary key: %#v", pk)
	}
	if got := string(src[pk.NameStart:pk.NameEnd]); got != "memberships_pk" {
		t.Fatalf("constraint name=%q", got)
	}
}

func TestParseRejectsMalformedTableLevelPrimaryKey(t *testing.T) {
	tests := []string{
		"CREATE TABLE t (a TEXT, PRIMARY KEY ())",
		"CREATE TABLE t (a TEXT, PRIMARY KEY (a,))",
		"CREATE TABLE t (a TEXT, PRIMARY KEY (a, a))",
		"CREATE TABLE t (a TEXT, PRIMARY KEY (a, A))",
		"CREATE TABLE t (a TEXT, PRIMARY KEY a)",
	}
	for _, src := range tests {
		var doc QueryDoc
		if err := Parse([]byte(src), &doc); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", src)
		}
	}
}
